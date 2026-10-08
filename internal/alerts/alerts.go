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
	// collects while busy appended to queued rather than discarded — see
	// Run and deliverLoop.
	deliveringMu sync.Mutex
	delivering   bool
	queued       []delivery
}

func NewChecker(s *store.Store, st *settings.Settings) *Checker {
	return &Checker{
		Store:    s,
		Settings: st,
		Client:   &http.Client{Timeout: 10 * time.Second},
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
// dropped here. Gated on MetricsEnabled,
// the same setting user_snapshots/feed_snapshots already use for this kind
// of periodic background collection — an admin who has turned dashboard
// history off has already said they don't want this class of background
// work running. A per-user failure is logged and skipped; it never aborts
// the rest of the batch.
func (c *Checker) Run(ctx context.Context) error {
	if !c.Settings.MetricsEnabled.Get() {
		return nil
	}
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
		pending, err := c.Store.PendingPrefixAlertDrop(ctx, u.ID, v4, v6)
		if err != nil {
			logging.Error("prefix alert check: pending lookup failed", "error", err, "user_id", u.ID)
			continue
		}
		if pending != nil {
			deliveries = append(deliveries, delivery{u, pending})
		}
	}

	if len(deliveries) == 0 {
		return nil
	}
	c.deliveringMu.Lock()
	if c.delivering {
		// A batch is already working through a backlog. A drop found again
		// here would just be rediscovered next tick via PendingPrefixAlertDrop
		// — but a recovery fires exactly once (EvaluateUserPrefixAlert has
		// already cleared alerting_since by the time it returns one), so
		// simply discarding it here would lose it permanently. Queue
		// instead: the running goroutine drains c.queued before it actually
		// stops (see below).
		c.queued = append(c.queued, deliveries...)
		c.deliveringMu.Unlock()
		return nil
	}
	c.delivering = true
	c.deliveringMu.Unlock()

	// ctx, not a fresh background one: deliveries are tied to the loop's own
	// lifetime (cancelled on shutdown), just no longer to this one tick's.
	go c.deliverLoop(ctx, webhookURL, deliveries)
	return nil
}

// deliverLoop delivers batch, then keeps draining whatever Run queued while
// it was busy (plain reslicing under deliveringMu, not a channel — batches
// are created at most once per tick and delivery is comparatively rare, so a
// small lock held only for a slice swap is simpler than a channel here).
func (c *Checker) deliverLoop(ctx context.Context, webhookURL string, batch []delivery) {
	for {
		for _, d := range batch {
			if d.transition.Event == store.PrefixAlertDrop {
				// A batch can sit queued across more than one measurement
				// tick (see above) — long enough for the episode it
				// describes to have already resolved, evaluated fresh by a
				// later tick while this one was still waiting its turn.
				// Sending it anyway would tell the receiver an outage is
				// still ongoing when it already isn't.
				stillPending, err := c.Store.PrefixAlertDropStillPending(ctx, d.user.ID)
				if err != nil {
					logging.Error("prefix alert check: pending re-check failed", "error", err, "user_id", d.user.ID)
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
			}
		}
		c.deliveringMu.Lock()
		if len(c.queued) == 0 {
			c.delivering = false
			c.deliveringMu.Unlock()
			return
		}
		batch, c.queued = c.queued, nil
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
// an error: the caller only needs to know whether to mark a drop delivered,
// not why a failure happened. A drop that still fails here stays pending —
// PendingPrefixAlertDrop offers it again next tick — but a lost recovery
// notification is an accepted limitation: there is no equivalent retry for
// it, since the episode it would describe is already over.
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
