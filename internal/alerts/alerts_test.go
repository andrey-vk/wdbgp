package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// waitForCalls polls until the webhook server has recorded at least n calls,
// or fails the test — delivery now runs in a detached goroutine (see Run's
// own comment on why), so a test can no longer assume it has already
// finished the instant Run itself returns.
func waitForCalls(t *testing.T, calls *[]webhookCall, mu *sync.Mutex, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*calls)
		mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d webhook call(s)", n)
}

func newTestChecker(t *testing.T) (*Checker, *store.Store, *settings.Settings) {
	t.Helper()
	// A real file, not ":memory:" — store.Open's connection pool allows up
	// to 4 connections (mainDBMaxOpenConns), and ":memory:" gives each one
	// its own separate, empty database unless using a shared-cache DSN.
	// Harmless for every other test here (one goroutine, one connection
	// reused throughout), but genuinely wrong for ones that have the
	// delivery goroutine and the test goroutine both querying concurrently.
	st, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite3"), false, "", false)
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

func TestCheckerRunIgnoresMetricsEnabled(t *testing.T) {
	// Withdraw alerting must not depend on the dashboard's own MetricsEnabled
	// toggle (a different feature, off by default) — an admin who sets
	// alert_webhook_url but never notices that unrelated setting must still
	// get alerted, not silently get nothing with no indication why.
	checker, st, set := newTestChecker(t)
	userID, err := st.AddUser(context.Background(), store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.MetricsEnabled.Set(context.Background(), false); err != nil {
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
		t.Fatal("expected history to still be recorded with MetricsEnabled false")
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
	waitForCalls(t, calls, mu, 1)

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
	mu.Unlock()

	// Still down on the next run: must not re-fire.
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(*calls) != 1 {
		t.Fatalf("webhook calls after a second run with no change = %d, want still 1", len(*calls))
	}
}

func TestCheckerRunRetriesAnUndeliveredDropOnceTheWebhookIsConfigured(t *testing.T) {
	checker, st, set := newTestChecker(t)
	srv, calls, mu := startWebhookServer(t)
	ctx := context.Background()
	if err := set.AlertPrefixDropThresholdPercent.Set(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := set.AlertPrefixBaselineMinimum.Set(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// AlertWebhookURL left empty: alerting is disabled.

	userID, err := st.AddUser(ctx, store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO user_prefix_alert_state(user_id, baseline_v4, baseline_v6, alerting_since, drop_delivered) VALUES (?, 100, 0, NULL, 1)", userID); err != nil {
		t.Fatal(err)
	}

	// A real drop happens while alerting is disabled. The state machine
	// still runs (baseline tracking can't stop just because delivery is
	// off), so alerting_since does get set — but drop_delivered stays
	// false, since nothing could actually tell anyone about it.
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := len(*calls)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("webhook calls while disabled = %d, want 0", n)
	}
	var alertingSince *int64
	var dropDelivered bool
	if err := st.DB.QueryRowContext(ctx, "SELECT alerting_since, drop_delivered FROM user_prefix_alert_state WHERE user_id = ?", userID).
		Scan(&alertingSince, &dropDelivered); err != nil {
		t.Fatal(err)
	}
	if alertingSince == nil {
		t.Fatal("alerting_since was not set: the state machine must keep running even while delivery is disabled")
	}
	if dropDelivered {
		t.Fatal("drop_delivered = true, want false: nothing could have delivered it")
	}

	// The webhook gets configured while the user is still down. The very
	// next run must retry and deliver the still-pending drop — not stay
	// silent, and not later report a "recovered" with no drop ever having
	// been announced for it.
	if err := set.AlertWebhookURL.Set(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	waitForCalls(t, calls, mu, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 {
		t.Fatalf("webhook calls after enabling mid-outage = %d, want 1 (the retried drop report)", len(*calls))
	}
	if (*calls)[0].payload.Event != string(store.PrefixAlertDrop) {
		t.Fatalf("event = %q, want %q", (*calls)[0].payload.Event, store.PrefixAlertDrop)
	}
}

func TestCheckerRunMeasuresAllUsersBeforeDeliveringAnyWebhook(t *testing.T) {
	checker, st, set := newTestChecker(t)
	ctx := context.Background()
	if err := set.AlertPrefixDropThresholdPercent.Set(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := set.AlertPrefixBaselineMinimum.Set(ctx, 1); err != nil {
		t.Fatal(err)
	}

	aliceID, err := st.AddUser(ctx, store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := st.AddUser(ctx, store.User{
		Name: "bob", PeerIP: "20.0.0.2", PeerASN: 65002, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{aliceID, bobID} {
		if _, err := st.DB.ExecContext(ctx,
			"INSERT INTO user_prefix_alert_state(user_id, baseline_v4, baseline_v6, alerting_since, drop_delivered) VALUES (?, 100, 0, NULL, 1)", id); err != nil {
			t.Fatal(err)
		}
	}

	// alice sorts before bob (lower user id, and Users(ctx, true) orders by
	// id), so her delivery is attempted first. By then, bob's measurement —
	// a plain DB write, nothing to do with the webhook — must already be
	// done, proving the measurement pass for every user completes before
	// any delivery is attempted, not interleaved user by user.
	var mu sync.Mutex
	var bobHistoryRowsWhenAliceDelivers int
	var aliceDelivered bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if p.UserID == aliceID {
			var n int
			if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_prefix_history WHERE user_id = ?", bobID).
				Scan(&n); err != nil {
				t.Error(err)
			}
			mu.Lock()
			bobHistoryRowsWhenAliceDelivers = n
			aliceDelivered = true
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := set.AlertWebhookURL.Set(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}

	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := aliceDelivered
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !aliceDelivered {
		t.Fatal("timed out waiting for alice's webhook delivery")
	}
	if bobHistoryRowsWhenAliceDelivers == 0 {
		t.Fatal("bob's history wasn't recorded yet by the time alice's webhook delivery ran: measurement must fully precede delivery, not interleave with it")
	}
}

func TestIsRetriableWebhookErrorClassifiesServerErrorsAndTooManyRequests(t *testing.T) {
	retriable := []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}
	for _, code := range retriable {
		if !isRetriableWebhookError(&httpStatusError{code: code}) {
			t.Errorf("status %d: want retriable", code)
		}
	}
	notRetriable := []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound}
	for _, code := range notRetriable {
		if isRetriableWebhookError(&httpStatusError{code: code}) {
			t.Errorf("status %d: want NOT retriable", code)
		}
	}
}

func TestCheckerDeliverRetriesATransientServerError(t *testing.T) {
	checker, _, _ := newTestChecker(t)
	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError) // not in retry.HTTPTransientError's own string set
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ok := checker.deliver(context.Background(), srv.URL, store.User{ID: 1, Name: "alice"},
		&store.PrefixAlertTransition{Event: store.PrefixAlertDrop, BaselineV4: 100, CurrentV4: 0})
	if !ok {
		t.Fatal("deliver returned false, want true: a 500 must be retried until the 3rd attempt succeeds")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestCheckerRunQueuesANewDeliveryInsteadOfLosingItWhileABatchIsBusy(t *testing.T) {
	// A real recovery can only happen for a user whose measured count goes
	// back up — which, for a user with no actual catalog selection (as
	// every user in this package's tests necessarily is, with no feed data
	// to select from), can never happen: CountSelectionPrefixes always
	// measures 0 for them, and a baseline can't recover to 0. So this
	// exercises the same at-risk branch (the queue-not-discard decision in
	// Run, which treats every delivery alike regardless of event type) with
	// two drops instead of a drop and a recovery: alice's is already
	// pending when the test starts, and bob's is detected fresh on the
	// second Run() call, made deliberately while alice's delivery is still
	// blocked — confirming bob's doesn't get silently discarded.
	checker, st, set := newTestChecker(t)
	ctx := context.Background()
	if err := set.AlertPrefixDropThresholdPercent.Set(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := set.AlertPrefixBaselineMinimum.Set(ctx, 1); err != nil {
		t.Fatal(err)
	}

	aliceID, err := st.AddUser(ctx, store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := st.AddUser(ctx, store.User{
		Name: "bob", PeerIP: "20.0.0.2", PeerASN: 65002, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// alice: a fresh, undelivered drop — her delivery request blocks below
	// to keep a batch "in flight" long enough for the second Run() call.
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO user_prefix_alert_state(user_id, baseline_v4, baseline_v6, alerting_since, drop_delivered) VALUES (?, 100, 0, ?, 0)",
		aliceID, time.Now().UTC().Unix()); err != nil {
		t.Fatal(err)
	}

	aliceBlock := make(chan struct{})
	var closeOnce sync.Once

	var mu sync.Mutex
	var events []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if p.UserID == aliceID {
			<-aliceBlock // held open until the test says bob's drop has been queued
		}
		mu.Lock()
		events = append(events, p.Event)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// A plain defer, registered after defer srv.Close() above so it runs
	// FIRST (defers are LIFO): t.Cleanup would run too late here — it fires
	// only after this goroutine has already exited, by which point the
	// defer above is already blocked in srv.Close() waiting for aliceBlock
	// to close, which is exactly what this is for. Guards against an
	// assertion below failing (t.Fatal) before the explicit close further
	// down ever runs, which would otherwise leave the handler — and so
	// httptest.Server.Close — blocked forever.
	defer closeOnce.Do(func() { close(aliceBlock) })
	if err := set.AlertWebhookURL.Set(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}

	// First Run(): bob has no alert-state row yet, so his first-ever
	// observation just establishes a baseline matching his (always 0,
	// selectionless) measured count — no transition. Alice's pending drop
	// is collected and starts delivering; her request blocks.
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		checker.deliveringMu.Lock()
		busy := checker.delivering
		checker.deliveringMu.Unlock()
		if busy {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Simulates bob having actually been fine until just now: his stored
	// baseline is bumped directly (CountSelectionPrefixes can't be made to
	// report anything but 0 for a selectionless test user, so his "drop" has
	// to be engineered this way instead). The second Run(), made while
	// alice's delivery is still blocked, measures his real (0) count against
	// this new baseline and detects a fresh drop.
	if _, err := st.DB.ExecContext(ctx, "UPDATE user_prefix_alert_state SET baseline_v4 = 100 WHERE user_id = ?", bobID); err != nil {
		t.Fatal(err)
	}
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}

	closeOnce.Do(func() { close(aliceBlock) })

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("events = %v, want 2 (alice's pending drop and bob's freshly-detected one, the latter queued rather than discarded while alice's batch was busy)", events)
	}
	for _, e := range events {
		if e != string(store.PrefixAlertDrop) {
			t.Fatalf("events = %v, want both to be drops", events)
		}
	}
}

func TestCheckerRunDeduplicatesQueuedDropsByUser(t *testing.T) {
	checker, st, set := newTestChecker(t)
	ctx := context.Background()
	if err := set.AlertPrefixDropThresholdPercent.Set(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := set.AlertPrefixBaselineMinimum.Set(ctx, 1); err != nil {
		t.Fatal(err)
	}

	aliceID, err := st.AddUser(ctx, store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := st.AddUser(ctx, store.User{
		Name: "bob", PeerIP: "20.0.0.2", PeerASN: 65002, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only alice's drop is seeded up front — she's the one whose delivery
	// blocks, keeping a batch "busy" for the rest of the test.
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO user_prefix_alert_state(user_id, baseline_v4, baseline_v6, alerting_since, drop_delivered) VALUES (?, 100, 0, ?, 0)",
		aliceID, time.Now().UTC().Unix()); err != nil {
		t.Fatal(err)
	}

	aliceBlock := make(chan struct{})
	var closeOnce sync.Once

	var mu sync.Mutex
	var bobDeliveries int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if p.UserID == aliceID {
			<-aliceBlock
		} else {
			mu.Lock()
			bobDeliveries++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// See the identical comment in the queueing test above for why this is
	// a plain defer (registered after defer srv.Close(), to run first) and
	// not t.Cleanup.
	defer closeOnce.Do(func() { close(aliceBlock) })
	if err := set.AlertWebhookURL.Set(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}

	// First Run(): bob has no alert-state row yet, so this just establishes
	// his baseline (matching his real, selectionless, always-0 count) — no
	// transition for him. Alice's pending drop starts delivering and blocks.
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		checker.deliveringMu.Lock()
		busy := checker.delivering
		checker.deliveringMu.Unlock()
		if busy {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Simulates bob having a single real drop, the same way the queueing
	// test above does (his count can't be made to actually change).
	if _, err := st.DB.ExecContext(ctx, "UPDATE user_prefix_alert_state SET baseline_v4 = 100 WHERE user_id = ?", bobID); err != nil {
		t.Fatal(err)
	}

	// One more tick: alice's delivery is still stuck, so her own pending
	// drop is rediscovered via PendingPrefixAlertDrop too — not just bob's,
	// fresh this time. Both land in c.queued, one entry each.
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	checker.deliveringMu.Lock()
	queuedLen := len(checker.queued)
	checker.deliveringMu.Unlock()
	if queuedLen != 2 {
		t.Fatalf("len(queued) = %d, want exactly 2 (alice and bob, one entry each)", queuedLen)
	}

	// Several more ticks while the batch is still stuck: the exact same two
	// still-undelivered drops are rediscovered every single time. Without
	// deduplication, c.queued would grow by two more copies per call —
	// confirmed by checking the count stays exactly where it was, not that
	// it merely stays small, since staying at a wrong-but-stable number
	// would hide the same class of bug.
	for i := 0; i < 5; i++ {
		if err := checker.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	checker.deliveringMu.Lock()
	queuedLen = len(checker.queued)
	checker.deliveringMu.Unlock()
	if queuedLen != 2 {
		t.Fatalf("len(queued) = %d, want still exactly 2 after 5 more ticks rediscovering the same two drops (no growth)", queuedLen)
	}

	closeOnce.Do(func() { close(aliceBlock) })
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := bobDeliveries
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if bobDeliveries != 1 {
		t.Fatalf("bob's drop was delivered %d times, want exactly 1", bobDeliveries)
	}
}

func TestCheckerDeliverLoopUsesTheCurrentWebhookURL(t *testing.T) {
	checker, st, set := newTestChecker(t)
	ctx := context.Background()
	if err := set.AlertPrefixDropThresholdPercent.Set(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if err := set.AlertPrefixBaselineMinimum.Set(ctx, 1); err != nil {
		t.Fatal(err)
	}

	aliceID, err := st.AddUser(ctx, store.User{
		Name: "alice", PeerIP: "20.0.0.1", PeerASN: 65001, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := st.AddUser(ctx, store.User{
		Name: "bob", PeerIP: "20.0.0.2", PeerASN: 65002, Enabled: true,
		FilterMode: store.FilterModeGlobal, CatalogModeID: store.DefaultCatalogModeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx,
		"INSERT INTO user_prefix_alert_state(user_id, baseline_v4, baseline_v6, alerting_since, drop_delivered) VALUES (?, 100, 0, ?, 0)",
		aliceID, time.Now().UTC().Unix()); err != nil {
		t.Fatal(err)
	}

	aliceBlock := make(chan struct{})
	var closeOnce sync.Once
	var mu sync.Mutex
	oldSrvHit := false
	newSrvHit := false

	oldSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		oldSrvHit = true
		mu.Unlock()
		<-aliceBlock
		w.WriteHeader(http.StatusOK)
	}))
	defer oldSrv.Close()

	newSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		newSrvHit = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer newSrv.Close()
	// See the identical comment in the queueing test above for why this is
	// a plain defer (registered after both defer ....Close() above, to run
	// before either) and not t.Cleanup.
	defer closeOnce.Do(func() { close(aliceBlock) })

	if err := set.AlertWebhookURL.Set(ctx, oldSrv.URL); err != nil {
		t.Fatal(err)
	}
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	// delivering itself is set synchronously inside Run, before the
	// goroutine even starts — polling it here wouldn't prove oldSrv's
	// handler has actually been reached yet, only that Run decided to
	// launch one. Poll the handler's own flag instead.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		hit := oldSrvHit
		mu.Unlock()
		if hit {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	hit := oldSrvHit
	mu.Unlock()
	if !hit {
		t.Fatal("alice's drop never reached oldSrv")
	}

	// The admin repoints the webhook while alice's delivery is still
	// in-flight, and bob's fresh drop is queued behind it. bob's own first
	// Run() call above (bundled into the same measurement pass as alice's)
	// already established his baseline at 0, his real selectionless
	// measured count — bumped here directly, the same technique the
	// queueing test above uses, to simulate him having actually been fine
	// until just now.
	if err := set.AlertWebhookURL.Set(ctx, newSrv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, "UPDATE user_prefix_alert_state SET baseline_v4 = 100 WHERE user_id = ?", bobID); err != nil {
		t.Fatal(err)
	}
	if err := checker.Run(ctx); err != nil {
		t.Fatal(err)
	}

	closeOnce.Do(func() { close(aliceBlock) })
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		hit := newSrvHit
		mu.Unlock()
		if hit {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !newSrvHit {
		t.Fatal("bob's drop never reached newSrv: deliverLoop kept using the URL captured when Run first launched it")
	}
}

func TestCheckerDeliverTreatsARedirectAsFailureNotSuccess(t *testing.T) {
	checker, _, _ := newTestChecker(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If the redirect were followed, this handler would see a bodyless
		// GET and could easily answer 200 — exactly the false "delivered"
		// deliver must not report.
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	ok := checker.deliver(context.Background(), redirector.URL, store.User{ID: 1, Name: "alice"},
		&store.PrefixAlertTransition{Event: store.PrefixAlertDrop, BaselineV4: 100, CurrentV4: 0})
	if ok {
		t.Fatal("deliver returned true for a redirected request: the payload was never actually received by the real target")
	}
}
