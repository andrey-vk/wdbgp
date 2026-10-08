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
	"fmt"
	"net/http"
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
}

func NewChecker(s *store.Store, st *settings.Settings) *Checker {
	return &Checker{
		Store:    s,
		Settings: st,
		Client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Run measures every enabled user once. Gated on MetricsEnabled, the same
// setting user_snapshots/feed_snapshots already use for this kind of
// periodic background collection — an admin who has turned dashboard
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

	for _, u := range users {
		v4, v6, err := c.Store.CountSelectionPrefixes(ctx, u.ID)
		if err != nil {
			logging.Error("prefix alert check: count failed", "error", err, "user_id", u.ID)
			continue
		}
		if err := c.Store.RecordUserPrefixSnapshot(ctx, u.ID, v4, v6); err != nil {
			logging.Error("prefix alert check: record snapshot failed", "error", err, "user_id", u.ID)
		}
		transition, err := c.Store.EvaluateUserPrefixAlert(ctx, u.ID, v4, v6, threshold, baselineMin)
		if err != nil {
			logging.Error("prefix alert check: evaluate failed", "error", err, "user_id", u.ID)
			continue
		}
		if transition == nil || webhookURL == "" {
			continue
		}
		c.deliver(ctx, webhookURL, u, transition)
	}
	return nil
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
		DetectedAt: time.Now().UTC().Format(time.RFC3339),
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

// deliver POSTs the transition as JSON, retrying transient failures
// (retry.HTTPConfig/HTTPTransientError — a few attempts over several
// seconds) and logging a final failure rather than returning it: a lost
// notification during a delivery outage is an accepted limitation here, not
// one this best-effort background loop can meaningfully recover from on its
// own — the next check still records history and still detects the next
// real transition.
func (c *Checker) deliver(ctx context.Context, webhookURL string, u store.User, t *store.PrefixAlertTransition) {
	body, err := json.Marshal(buildPayload(u, t))
	if err != nil {
		logging.Error("prefix alert webhook: marshal failed", "error", err, "user_id", u.ID)
		return
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
			return fmt.Errorf("webhook responded %d", resp.StatusCode)
		}
		return nil
	}, retry.HTTPTransientError)
	if err != nil {
		logging.Error("prefix alert webhook delivery failed", "error", err, "user_id", u.ID, "event", string(t.Event))
	}
}
