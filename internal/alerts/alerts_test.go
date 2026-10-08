package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func newTestChecker(t *testing.T) (*Checker, *store.Store, *settings.Settings) {
	t.Helper()
	st, err := store.Open(":memory:", false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})
	set, err := settings.New(settings.NewTestStore())
	if err != nil {
		t.Fatal(err)
	}
	if err := set.MetricsEnabled.Set(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	return NewChecker(st, set), st, set
}

type webhookCall struct {
	payload webhookPayload
}

func startWebhookServer(t *testing.T) (*httptest.Server, *[]webhookCall, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var calls []webhookCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		mu.Lock()
		calls = append(calls, webhookCall{payload: p})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &mu
}

func TestCheckerRunSkipsEverythingWhenMetricsDisabled(t *testing.T) {
	checker, st, set := newTestChecker(t)
	if _, err := st.AddUser(context.Background(), store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := set.MetricsEnabled.Set(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	if err := checker.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	history, err := st.UserPrefixHistory(context.Background(), 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatal("expected no history recorded while MetricsEnabled is false")
	}
}

func TestCheckerRunRecordsHistoryWithoutAWebhookConfigured(t *testing.T) {
	checker, st, _ := newTestChecker(t)
	userID, err := st.AddUser(context.Background(), store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
		Networks: []string{"203.0.113.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := checker.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	history, err := st.UserPrefixHistory(context.Background(), userID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %+v, want one recorded snapshot even with alerting disabled", history)
	}
}

func TestCheckerRunDeliversDropWebhook(t *testing.T) {
	// The store-level drop/recovery state machine itself is already
	// thoroughly covered in internal/store/user_prefix_alerts_test.go
	// (EvaluateUserPrefixAlert, directly, on contrived counts). This test
	// is about the wiring Run does around it: Count -> Record -> Evaluate
	// -> deliver, ending in an actual HTTP POST with the expected shape —
	// not about exercising CountSelectionPrefixes' own catalog-driven
	// counting (a fresh user with no selection always counts 0, so a
	// pre-seeded baseline row is used to force a deterministic drop).
	checker, st, set := newTestChecker(t)
	srv, calls, mu := startWebhookServer(t)
	ctx := context.Background()
	if err := set.AlertWebhookURL.Set(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := set.AlertPrefixDropThresholdPercent.Set(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := set.AlertPrefixBaselineMinimum.Set(ctx, 1); err != nil {
		t.Fatal(err)
	}

	userID, err := st.AddUser(ctx, store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO user_prefix_alert_state(user_id, baseline_v4, baseline_v6, alerting_since) VALUES (?, 100, 0, NULL)", userID); err != nil {
		t.Fatal(err)
	}

	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 {
		t.Fatalf("webhook calls = %d, want 1", len(*calls))
	}
	p := (*calls)[0].payload
	if p.Event != string(store.PrefixAlertDrop) {
		t.Fatalf("event = %q, want %q", p.Event, store.PrefixAlertDrop)
	}
	if p.UserID != userID || p.UserName != "alice" {
		t.Fatalf("payload user = %d/%q, want %d/alice", p.UserID, p.UserName, userID)
	}
	if p.BaselineV4 != 100 || p.CurrentV4 != 0 {
		t.Fatalf("payload baseline/current v4 = %d/%d, want 100/0", p.BaselineV4, p.CurrentV4)
	}
	if p.DropPercent != 100 {
		t.Fatalf("drop_percent = %d, want 100", p.DropPercent)
	}

	// Still down on the next run: must not re-fire.
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("webhook calls after a second run with no change = %d, want still 1", len(*calls))
	}
}
