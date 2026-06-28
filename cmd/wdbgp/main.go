package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrey-vk/wdbgp/internal/bgp"
	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
	"github.com/andrey-vk/wdbgp/internal/web"
)

func main() {
	if err := run(); err != nil {
		logging.Fatal("application error", "error", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Configure logging based on config
	logging.Configure(cfg.LogLevel, cfg.LogFormat)

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "healthcheck" {
		return healthcheck(cfg)
	}

	db, err := store.Open(cfg.DBPath, cfg)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck // process is about to exit, Close is best-effort

	// Apply DB-stored settings (ENV overrides are preserved)
	if dbSettings, err := db.GetAllSettings(context.Background()); err == nil {
		cfg.ApplyDBOverrides(dbSettings)
	}

	switch command {
	case "migrate", "init":
		logging.Info("database migrations are up to date")
		return nil
	case "stats":
		return printStats(context.Background(), db)
	case "sync":
		syncer := feeds.NewSyncer(db, cfg)
		if syncErrors := syncer.SyncAll(context.Background()); len(syncErrors) > 0 {
			for _, syncErr := range syncErrors {
				logging.Error("feed sync error", "error", syncErr)
			}
			return fmt.Errorf("%d feed(s) failed", len(syncErrors))
		}
		return printStats(context.Background(), db)
	case "serve":
		return serve(cfg, db)
	default:
		return fmt.Errorf("unknown command %q; use serve, migrate, sync, stats, or healthcheck", command)
	}
}

func serve(cfg config.Config, db *store.Store) error {
	if db.Degraded {
		return serveDegraded(cfg, db)
	}

	if err := cfg.ValidateServe(); err != nil {
		return err
	}

	logging.Info("starting application",
		"bgp_asn", cfg.LocalASN,
		"bgp_port", cfg.BGPListenPort,
		"http_address", cfg.ListenAddress(),
		"sync_interval", cfg.SyncInterval,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	bgpManager := bgp.NewManager(cfg, db)
	if err := bgpManager.Start(ctx); err != nil {
		return fmt.Errorf("start BGP: %w", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := bgpManager.Stop(stopCtx); err != nil {
			logging.Error("failed to stop BGP manager", "error", err)
		}
	}()

	syncer := feeds.NewSyncer(db, cfg)
	go syncLoop(ctx, cfg.SyncInterval, syncer, bgpManager)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           web.New(cfg, db, syncer, bgpManager).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logging.Info("HTTP server starting",
			"address", cfg.ListenAddress(),
			"bgp_asn", cfg.LocalASN,
			"bgp_port", cfg.BGPListenPort,
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

// serveDegraded starts an HTTP server that only shows the DB version mismatch page.
func serveDegraded(cfg config.Config, db *store.Store) error {
	logging.Warn("starting in degraded mode",
		"db_version", db.DBVersion, "server_version", db.ServerVersion)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := web.New(cfg, db, nil, nil)
	srv.SetDegraded(web.DegradedInfo{
		CurrentVersion: db.DBVersion,
		ServerVersion:  db.ServerVersion,
		Reason:         db.DegradedReason,
	})

	httpServer := &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logging.Info("HTTP server starting in degraded mode",
			"address", cfg.ListenAddress(),
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

func syncLoop(ctx context.Context, interval time.Duration, syncer *feeds.Syncer, manager *bgp.Manager) {
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
	}
	syncNow()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
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
		status := feed.LastSuccess
		if status == "" {
			status = "never"
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

func healthcheck(cfg config.Config) error {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port))
	if err != nil {
		return err
	}
	defer response.Body.Close() //nolint:errcheck // health check response body, no data to read
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
