package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/andrey-vk/wdbgp/internal/bgp"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
	"github.com/andrey-vk/wdbgp/internal/web"
)

func main() {
	if err := run(); err != nil {
		logging.Fatal("application error", "error", err)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	// healthcheck doesn't need store or settings — just probes HTTP endpoint
	if command == "healthcheck" {
		return healthcheck()
	}

	// Determine DBPath from env so we can open the store.
	dbPath := os.Getenv("WDBGP_DB")
	if dbPath == "" {
		dbPath = "/data/wdbgp.sqlite3"
	}
	// Validated here, before store.Open touches the filesystem — by the
	// time settings.New() runs (which also validates DBPath), the store
	// has already been opened once from this same value, so validating
	// only there would be too late to actually fail fast.
	if err := settings.ValidateDBPath(dbPath); err != nil {
		return fmt.Errorf("WDBGP_DB=%q: %w", dbPath, err)
	}

	// Read backup/env-only settings from env for store.Open.
	backupEnabled := envBool("WDBGP_BACKUP_ENABLED", true)
	backupDir := os.Getenv("WDBGP_BACKUP_DIR")
	if backupDir == "" {
		backupDir = dbPath[:max(lastSlash(dbPath), 0)]
		if backupDir == "" {
			backupDir = "."
		}
	}
	autoRestore := envBool("WDBGP_AUTO_RESTORE_ENABLED", false)

	db, err := store.Open(dbPath, backupEnabled, backupDir, autoRestore)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck // process is about to exit, Close is best-effort

	// Create settings with the store (loads DB values and merges with env).
	s, err := settings.New(db)
	if err != nil {
		return err
	}

	// Configure logging based on settings
	logging.Configure(s.LogLevel.Get(), s.LogFormat.Get())

	switch command {
	case "migrate", "init":
		logging.Info("database migrations are up to date")
		return nil
	case "stats":
		return printStats(context.Background(), db)
	case "sync":
		syncer := feeds.NewSyncer(db, s)
		if syncErrors := syncer.SyncAll(context.Background()); len(syncErrors) > 0 {
			for _, syncErr := range syncErrors {
				logging.Error("feed sync error", "error", syncErr)
			}
			return fmt.Errorf("%d feed(s) failed", len(syncErrors))
		}
		return printStats(context.Background(), db)
	case "serve":
		return serve(s, db)
	default:
		return fmt.Errorf("unknown command %q; use serve, migrate, sync, stats, or healthcheck", command)
	}
}

func serve(s *settings.Settings, db *store.Store) error {
	if db.Degraded {
		return serveDegraded(s, db)
	}

	// Require admin secrets before serving
	if s.AdminPassword.Get() == "" || s.SessionSecret.Get() == "" {
		return fmt.Errorf("WDBGP_ADMIN_PASSWORD and WDBGP_SESSION_SECRET are required")
	}

	logging.Info("starting application",
		"bgp_asn", s.LocalASN.Get(),
		"bgp_port", s.BGPPort.Get(),
		"http_address", fmt.Sprintf("%s:%d", s.Host.Get(), s.Port.Get()),
		"sync_interval", s.SyncInterval.Get(),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// The NFQUEUE redirect rule for dynamic-peer MD5 is managed by the BGP
	// speaker, paired with the queue consumer's lifetime (see Speaker.Start).
	// The one case the speaker never sees: the feature was enabled in a
	// previous run and is disabled now. In a host network namespace that
	// run's rule survives process death, and with no consumer attached it
	// drops every inbound BGP SYN — so clear any leftover here.
	if !s.DynamicPeerMD5Match.Get() {
		if err := bgp.RemoveDynamicMD5NFQueueRule(); err != nil {
			logging.Debug("cleanup of leftover dynamic-peer MD5 NFQUEUE rule failed (harmless unless the feature was previously enabled)", "error", err)
		}
	}

	bgpManager := bgp.NewManager(s, db)
	if err := bgpManager.Start(ctx); err != nil {
		// Not fatal: the web UI must stay reachable so an admin can see
		// what's wrong (via the BGP status banner) and fix the setting
		// without needing shell/redeploy access. bgpManager.Status()
		// reflects this failure for the rest of the process's life until
		// a reload succeeds.
		logging.Error("BGP manager failed to start, continuing with BGP down", "error", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := bgpManager.Stop(stopCtx); err != nil {
			logging.Error("failed to stop BGP manager", "error", err)
		}
	}()

	syncer := feeds.NewSyncer(db, s)
	go syncLoop(ctx, time.Duration(s.SyncInterval.Get())*time.Second, syncer, bgpManager, db, s)
	go purgeLoop(ctx, time.Hour, db, s)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.Host.Get(), s.Port.Get()),
		Handler:           web.New(s, db, syncer, bgpManager).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	port := fmt.Sprintf("%d", s.Port.Get())
	if err := os.WriteFile(portFilePath(s.DBPath.Get()), []byte(port), 0600); err != nil {
		return fmt.Errorf("write port file: %w", err)
	}

	go func() {
		logging.Info("HTTP server starting",
			"address", fmt.Sprintf("%s:%d", s.Host.Get(), s.Port.Get()),
			"bgp_asn", s.LocalASN.Get(),
			"bgp_port", s.BGPPort.Get(),
		)
		serverErrors <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logging.Info("shutdown signal received")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	logging.Info("shutting down HTTP server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdownCtx)
}

// purgeLoop periodically removes old metric snapshots based on metrics_history_days.
func purgeLoop(ctx context.Context, interval time.Duration, db *store.Store, s *settings.Settings) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.MetricsEnabled.Get() {
				continue
			}
			days := s.MetricsHistoryDays.Get()
			if days <= 0 {
				days = 14
			}
			if err := db.PurgeUserSnapshots(ctx, days); err != nil {
				logging.Error("metrics purge failed for user snapshots", "error", err)
			}
			if err := db.PurgeFeedSnapshots(ctx, days); err != nil {
				logging.Error("metrics purge failed for feed snapshots", "error", err)
			}
		}
	}
}

// serveDegraded starts an HTTP server that only shows the DB version mismatch page.
func serveDegraded(s *settings.Settings, db *store.Store) error {
	logging.Warn("starting in degraded mode",
		"db_version", db.DBVersion, "server_version", db.ServerVersion)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := web.New(s, db, nil, nil)
	srv.SetDegraded(web.DegradedInfo{
		CurrentVersion: db.DBVersion,
		ServerVersion:  db.ServerVersion,
		Reason:         db.DegradedReason,
	})

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.Host.Get(), s.Port.Get()),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logging.Info("HTTP server starting in degraded mode",
			"address", fmt.Sprintf("%s:%d", s.Host.Get(), s.Port.Get()),
		)
		serverErrors <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logging.Info("shutdown signal received")
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	logging.Info("shutting down HTTP server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdownCtx)
}

func syncLoop(ctx context.Context, interval time.Duration, syncer *feeds.Syncer, manager *bgp.Manager, db *store.Store, s *settings.Settings) {
	syncNow := func() {
		logger := logging.FromContext(ctx)
		logger.Info("starting feed sync")

		errors := syncer.SyncAll(ctx)
		if len(errors) > 0 {
			for _, err := range errors {
				logger.Error("feed sync error", "error", err)
			}
			logger.Error("feed sync completed with errors", "error_count", len(errors))
		} else {
			logger.Info("feed sync completed successfully")
		}

		if err := manager.Reconcile(ctx); err != nil && ctx.Err() == nil {
			logger.Error("BGP reconcile error", "error", err)
		} else if ctx.Err() == nil {
			logger.Info("BGP reconcile completed")
		}

		db.RecordFeedSnapshot(ctx, s.MetricsEnabled.Get())
		peerStates, _ := manager.PeerStates(ctx) //nolint:errcheck
		db.RecordUserSnapshot(ctx, s.MetricsEnabled.Get(), peerStates)
	}
	syncNow()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// sync_interval is saved through the settings API without any restart
	// requirement, but the ticker was only ever built once at startup from
	// the interval captured here — a saved change had no runtime effect
	// until the next process restart. Reset the ticker in place when it
	// changes, the same way RateLimitLogin/RateLimitAdmin already reload
	// live (see web.New's OnChange registrations).
	unsubscribe := s.SyncInterval.OnChange(func(v int) {
		if v > 0 {
			ticker.Reset(time.Duration(v) * time.Second)
		}
	})
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			logging.FromContext(ctx).Info("sync loop stopping")
			return
		case <-ticker.C:
			syncNow()
		}
	}
}

func printStats(ctx context.Context, db *store.Store) error {
	categories, services, entries, err := db.Stats(ctx)
	if err != nil {
		return err
	}

	logger := logging.FromContext(ctx)
	logger.Info("catalog statistics",
		"categories", categories,
		"services", services,
		"entries", entries,
	)

	feedList, err := db.Feeds(ctx, false)
	if err != nil {
		return err
	}

	for _, feed := range feedList {
		status := "never"
		if feed.LastSuccess > 0 {
			status = time.Unix(feed.LastSuccess, 0).UTC().Format(time.RFC3339)
		}
		if feed.LastError != "" {
			status = "error: " + feed.LastError
		}
		logger.Info("feed status",
			"name", feed.Name,
			"status", status,
			"enabled", feed.Enabled,
		)
	}
	return nil
}

func healthcheck() error {
	dbPath := os.Getenv("WDBGP_DB")
	if dbPath == "" {
		dbPath = "/data/wdbgp.sqlite3"
	}
	port, err := os.ReadFile(portFilePath(dbPath))
	if err != nil {
		return fmt.Errorf("read port file: %w", err)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + string(port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // process exits immediately after, Close is best-effort
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	return nil
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// portFilePath returns where the HTTP listen port is recorded so that a
// separate `wdbgp healthcheck` invocation can find it. It lives next to the
// database rather than in the shared /tmp, since /tmp isn't necessarily
// exclusive to this process (gosec G303).
func portFilePath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "wdbgp-port")
}
