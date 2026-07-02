package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrey-vk/wdbgp/internal/bgp"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// TestSyncLoopReloadsIntervalLive guards against a bug where sync_interval
// saved through the settings API had no runtime effect until a full process
// restart — the ticker driving syncLoop was only ever built once at startup
// from the interval captured at that time. Starts the loop with a long
// (1 hour) interval, then lowers sync_interval to 1 second at runtime and
// confirms a second sync actually happens well before the original 1 hour
// would have elapsed.
func TestSyncLoopReloadsIntervalLive(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "synctest.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	// Disable every built-in feed so SyncAll completes instantly instead of
	// making real HTTP requests to the seeded feed URLs.
	if _, err := db.DB.ExecContext(ctx, "UPDATE feeds SET enabled = 0"); err != nil {
		t.Fatal(err)
	}

	s, err := settings.New(settings.NewTestStoreWith(map[string]string{
		"local_asn": "64512", "router_id": "192.0.2.1", "sync_interval": "3600",
	}))
	if err != nil {
		t.Fatal(err)
	}

	syncer := feeds.NewSyncer(db, s)
	manager := bgp.NewManager(s, db) // never Start()ed — Reconcile just errors harmlessly

	var buf bytes.Buffer
	testLogger := &logging.Logger{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	runCtx, cancel := context.WithCancel(logging.WithLogger(ctx, testLogger))
	defer cancel()

	done := make(chan struct{})
	go func() {
		syncLoop(runCtx, time.Duration(s.SyncInterval.Get())*time.Second, syncer, manager, db, s)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Wait for the initial up-front sync (syncLoop calls syncNow() once
	// before entering the ticker loop).
	deadline := time.Now().Add(2 * time.Second)
	for strings.Count(buf.String(), "starting feed sync") < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	countAfterInitial := strings.Count(buf.String(), "starting feed sync")
	if countAfterInitial < 1 {
		t.Fatal("initial sync never ran")
	}

	// Lower sync_interval to 1 second at runtime — without the fix, the
	// ticker keeps firing on the original 1 hour interval and a second sync
	// would never happen within this test's timeout.
	if err := s.SyncInterval.Set(ctx, 1); err != nil {
		t.Fatal(err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for strings.Count(buf.String(), "starting feed sync") <= countAfterInitial && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := strings.Count(buf.String(), "starting feed sync"); got <= countAfterInitial {
		t.Fatalf("sync count = %d after lowering sync_interval to 1s, want > %d (a new sync should have run within ~1s, not wait out the original 1 hour interval)", got, countAfterInitial)
	}
}
