// Package alerts periodically measures each enabled user's effective
// announced prefix count, records it for the admin "prefix history" view,
// and pushes a webhook when it detects a sudden drop or recovery — issue
// #49 item #10: "User 23 lost 90% of its prefixes at 03:14 should be
// pushed, not discovered via a phone call."
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/retry"
	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// Checker ties the store's prefix-counting and alert-state primitives to an
// outbound webhook. Nothing here mutates routing or BGP state — it is
// read-and-notify only.
type Checker struct {
	Store    *store.Store
	Settings *settings.Settings
	Client   *http.Client

	// deliveringMu guards delivering and queued together: at most one
	// in-flight delivery batch at a time (a second concurrent one would
	// risk double-delivering the same pending drop), with anything Run
	// collects while busy merged into queued rather than discarded — see
	// Run and deliverLoop. queued is keyed by user ID, not a plain slice: a
	// drop still undelivered after several ticks is rediscovered by every
	// one of them (PendingPrefixAlertDrop has no memory of what's already
	// queued), and a plain append would grow queued by one entry per tick
	// for as long as delivery stays slower than the check interval — the
	// map just holds the latest transition for that user instead.
	deliveringMu sync.Mutex
	delivering   bool
	queued       map[int64]delivery
}

func NewChecker(s *store.Store, st *settings.Settings) *Checker {
	return &Checker{
		Store:    s,
		Settings: st,
		Client: &http.Client{
			Timeout: 10 * time.Second,
			// Go's default redirect handling turns a 301/302/303 into a
			// bodyless GET — silently dropping the JSON payload. A
			// redirected endpoint that then answers 2xx would make deliver
			// report success for an alert the receiver never actually got
			// (e.g. a webhook URL entered as http:// that the server
			// redirects to https://). Returning the redirect response
			// itself instead means deliver's own resp.StatusCode >= 300
			// check already treats it as a failure, same as any other
			// non-2xx.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// delivery is one (user, transition) pair Run collected during its
// measurement pass, still needing a webhook attempt.
type delivery struct {
	user       store.User
	transition *store.PrefixAlertTransition
}

// Run measures every enabled user once, then hands any resulting webhooks
// off to a detached goroutine instead of delivering them itself — Run
// returns as soon as measurement is done, so a slow or unreachable webhook
// endpoint can never delay the *next* tick's measurement either (collecting
// every user's deliveries before attempting any, on its own, only protects
// users within the same tick — prefixAlertLoop still calls Run itself on a
// fixed ticker, and would be delayed exactly as much if Run blocked on
// delivery before returning). At most one delivery batch runs at a time
// (deliveringMu/delivering): if the previous tick's batch is still working
// through a backlog, this tick's deliveries are queued for that same
// goroutine to pick up once it's done with its current pass, rather than
// attempting a second concurrent batch or discarding them outright — a drop
// would simply be rediscovered next tick via PendingPrefixAlertDrop either
// way, but a recovery fires exactly once and would be lost for good if
// dropped here.
//
// Deliberately NOT gated on MetricsEnabled: that setting controls the
// dashboard's own background collection (user_snapshots/feed_snapshots),
// a different feature this one doesn't otherwise depend on. Gating withdraw
// alerting on it too would mean an admin who sets alert_webhook_url (and
// leaves MetricsEnabled at its default of off, since the two have no
// obvious connection by name) gets no alerts at all, with nothing anywhere
// to say why — exactly the kind of silent failure this feature exists to
// avoid in the first place. A per-user failure is logged and skipped; it
// never aborts the rest of the batch.
func (c *Checker) Run(ctx context.Context) error {
	users, err := c.Store.Users(ctx, true)
	if err != nil {
		return fmt.Errorf("alerts: list users: %w", err)
	}
	webhookURL := c.Settings.AlertWebhookURL.Get()
	threshold := c.Settings.AlertPrefixDropThresholdPercent.Get()
	baselineMin := c.Settings.AlertPrefixBaselineMinimum.Get()

	var deliveries []delivery
	for _, u := range users {
		v4, v6, err := c.Store.CountSelectionPrefixes(ctx, u.ID)
		if err != nil {
			logging.Error("prefix alert check: count failed", "error", err, "user_id", u.ID)
			continue
		}
		if err := c.Store.RecordUserPrefixSnapshot(ctx, u.ID, v4, v6); err != nil {
			logging.Error("prefix alert check: record snapshot failed", "error", err, "user_id", u.ID)
		}
		// Always evaluated, regardless of whether a webhook is configured —
		// the baseline has to stay current, and a drop detected while
		// alerting is disabled still needs to be on record as having
		// happened (see PendingPrefixAlertDrop) rather than silently
		// adopting whatever's current as the new "normal" the next time
		// anyone looks.
		transition, err := c.Store.EvaluateUserPrefixAlert(ctx, u.ID, v4, v6, threshold, baselineMin)
		if err != nil {
			logging.Error("prefix alert check: evaluate failed", "error", err, "user_id", u.ID)
			continue
		}
		if webhookURL == "" {
			continue
		}
		if transition != nil {
			deliveries = append(deliveries, delivery{u, transition})
			continue
		}
		// No new transition this tick — but an earlier drop for this user
		// may still be sitting undelivered (alerting was off, or an
		// earlier delivery attempt failed, when it first happened). Retry
		// it now, with this tick's freshly measured count rather than a
		// stale one.
		pendingDrop, err := c.Store.PendingPrefixAlertDrop(ctx, u.ID, v4, v6)
		if err != nil {
			logging.Error("prefix alert check: pending lookup failed", "error", err, "user_id", u.ID)
			continue
		}
		if pendingDrop != nil {
			deliveries = append(deliveries, delivery{u, pendingDrop})
			continue
		}
		// Same, for a recovery whose own webhook never got through. A user
		// can only have one of these pending at a time by construction
		// (EvaluateUserPrefixAlert clears recovery_pending the moment a
		// fresh drop supersedes it), so checking both costs nothing extra.
		pendingRecovery, err := c.Store.PendingPrefixAlertRecovery(ctx, u.ID, v4, v6)
		if err != nil {
			logging.Error("prefix alert check: pending recovery lookup failed", "error", err, "user_id", u.ID)
			continue
		}
		if pendingRecovery != nil {
			deliveries = append(deliveries, delivery{u, pendingRecovery})
		}
	}

	if len(deliveries) == 0 {
		return nil
	}
	c.deliveringMu.Lock()
	if c.delivering {
		// A batch is already working through a backlog. Either event found
		// again here would be rediscovered on a later tick via
		// PendingPrefixAlertDrop/PendingPrefixAlertRecovery, but merging it
		// into c.queued now (by user, last one wins) delivers it sooner
		// instead of waiting for that rediscovery. The running goroutine
		// drains c.queued before it actually stops (see below).
		if c.queued == nil {
			c.queued = make(map[int64]delivery, len(deliveries))
		}
		for _, d := range deliveries {
			c.queued[d.user.ID] = d
		}
		c.deliveringMu.Unlock()
		return nil
	}
	c.delivering = true
	c.deliveringMu.Unlock()

	// ctx, not a fresh background one: deliveries are tied to the loop's own
	// lifetime (cancelled on shutdown), just no longer to this one tick's.
	go c.deliverLoop(ctx, deliveries)
	return nil
}

// deliverLoop delivers batch, then keeps draining whatever Run queued while
// it was busy (a map swap under deliveringMu, not a channel — batches are
// created at most once per tick and delivery is comparatively rare, so a
// small lock held only for the swap is simpler than a channel here). The
// webhook URL is re-read fresh on every pass, not just once when Run first
// launched this goroutine — an admin changing or clearing it while a batch
// is still working through a backlog must take effect on whatever's still
// queued, not keep going to a URL that's no longer current.
func (c *Checker) deliverLoop(ctx context.Context, batch []delivery) {
	for {
		webhookURL := c.Settings.AlertWebhookURL.Get()
		if webhookURL == "" {
			// Alerting was disabled mid-backlog. Whatever's left in batch
			// (and in c.queued) stays correctly represented by the
			// persisted DB state (drop_delivered still 0 for any undelivered
			// drop), so nothing here needs to survive this goroutine ending
			// — a future Run, once a URL is configured again, rediscovers
			// it via PendingPrefixAlertDrop same as always.
			c.deliveringMu.Lock()
			c.delivering = false
			c.queued = nil
			c.deliveringMu.Unlock()
			return
		}
		for _, d := range batch {
			// A batch can sit queued across more than one measurement
			// tick (see above) — long enough for the episode it
			// describes to have already resolved, evaluated fresh by a
			// later tick while this one was still waiting its turn.
			// Sending it anyway would tell the receiver an outage (or a
			// recovery) is still current when it already isn't.
			if d.transition.Event == store.PrefixAlertDrop {
				stillPending, err := c.Store.PrefixAlertDropStillPending(ctx, d.user.ID)
				if err != nil {
					logging.Error("prefix alert check: pending re-check failed", "error", err, "user_id", d.user.ID)
					continue
				}
				if !stillPending {
					continue
				}
			} else {
				stillPending, err := c.Store.PrefixAlertRecoveryStillPending(ctx, d.user.ID)
				if err != nil {
					logging.Error("prefix alert check: pending recovery re-check failed", "error", err, "user_id", d.user.ID)
					continue
				}
				if !stillPending {
					continue
				}
			}
			if !c.deliver(ctx, webhookURL, d.user, d.transition) {
				continue
			}
			if d.transition.Event == store.PrefixAlertDrop {
				if err := c.Store.MarkPrefixAlertDropDelivered(ctx, d.user.ID); err != nil {
					logging.Error("prefix alert check: mark delivered failed", "error", err, "user_id", d.user.ID)
				}
			} else {
				if err := c.Store.MarkPrefixAlertRecoveryDelivered(ctx, d.user.ID); err != nil {
					logging.Error("prefix alert check: mark recovery delivered failed", "error", err, "user_id", d.user.ID)
				}
			}
		}
		c.deliveringMu.Lock()
		if len(c.queued) == 0 {
			c.delivering = false
			c.deliveringMu.Unlock()
			return
		}
		batch = make([]delivery, 0, len(c.queued))
		for _, d := range c.queued {
			batch = append(batch, d)
		}
		c.queued = nil
		c.deliveringMu.Unlock()
	}
}

type webhookPayload struct {
	Event           string `json:"event"`
	UserID          int64  `json:"user_id"`
	UserName        string `json:"user_name"`
	DetectedAt      string `json:"detected_at"`
	BaselineV4      int    `json:"baseline_v4"`
	BaselineV6      int    `json:"baseline_v6"`
	CurrentV4       int    `json:"current_v4"`
	CurrentV6       int    `json:"current_v6"`
	DropPercent     int    `json:"drop_percent,omitempty"`
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
}

func buildPayload(u store.User, t *store.PrefixAlertTransition) webhookPayload {
	p := webhookPayload{
		Event:      string(t.Event),
		UserID:     u.ID,
		UserName:   u.Name,
		DetectedAt: time.Unix(t.DetectedAt, 0).UTC().Format(time.RFC3339),
		BaselineV4: t.BaselineV4,
		BaselineV6: t.BaselineV6,
		CurrentV4:  t.CurrentV4,
		CurrentV6:  t.CurrentV6,
	}
	if t.Event == store.PrefixAlertDrop {
		baselineTotal := t.BaselineV4 + t.BaselineV6
		currentTotal := t.CurrentV4 + t.CurrentV6
		if baselineTotal > 0 {
			p.DropPercent = 100 - currentTotal*100/baselineTotal
		}
	} else {
		p.DurationSeconds = t.DurationSeconds
	}
	return p
}

// httpStatusError lets isRetriableWebhookError classify a non-2xx response
// by its actual numeric status, rather than depending on retry.
// HTTPTransientError's generic substring matching — which recognizes 429,
// 503, and 504 by their literal digits (and a handful of textual phrases),
// but not 500 or 502, both of which are just as transient in practice.
type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("webhook responded %d", e.code) }

func isRetriableWebhookError(err error) bool {
	var se *httpStatusError
	if errors.As(err, &se) {
		return se.code == http.StatusTooManyRequests || se.code >= http.StatusInternalServerError
	}
	// Not an HTTP status (connection refused, timeout, DNS failure, ...):
	// retry.HTTPTransientError's own string-based classification already
	// covers these network-level cases.
	return retry.HTTPTransientError(err)
}

// deliver POSTs the transition as JSON, retrying a transient failure
// (isRetriableWebhookError — a few attempts over several seconds via
// retry.HTTPConfig) and logging a final failure rather than returning it as
// an error: the caller only needs to know whether to mark it delivered, not
// why a failure happened. Either event that still fails here stays
// pending — a drop via PendingPrefixAlertDrop, a recovery via
// PendingPrefixAlertRecovery — and is offered again on a later tick.
func (c *Checker) deliver(ctx context.Context, webhookURL string, u store.User, t *store.PrefixAlertTransition) bool {
	body, err := json.Marshal(buildPayload(u, t))
	if err != nil {
		logging.Error("prefix alert webhook: marshal failed", "error", err, "user_id", u.ID)
		return false
	}
	err = retry.Do(ctx, retry.HTTPConfig, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }() //nolint:errcheck
		if resp.StatusCode >= 300 {
			return &httpStatusError{code: resp.StatusCode}
		}
		return nil
	}, isRetriableWebhookError)
	if err != nil {
		logging.Error("prefix alert webhook delivery failed", "error", err, "user_id", u.ID, "event", string(t.Event))
		return false
	}
	return true
}
