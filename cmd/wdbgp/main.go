package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrey-vk/wdbgp/internal/bgp"
	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/store"
	"github.com/andrey-vk/wdbgp/internal/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "healthcheck" {
		return healthcheck(cfg)
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	switch command {
	case "migrate", "init":
		fmt.Println("database migrations are up to date")
		return nil
	case "stats":
		return printStats(context.Background(), db)
	case "sync":
		syncer := feeds.NewSyncer(db)
		if syncErrors := syncer.SyncAll(context.Background()); len(syncErrors) > 0 {
			for _, syncErr := range syncErrors {
				log.Print(syncErr)
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
	if err := cfg.ValidateServe(); err != nil {
		return err
	}
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
			log.Printf("stop BGP: %v", err)
		}
	}()

	syncer := feeds.NewSyncer(db)
	go syncLoop(ctx, cfg.SyncInterval, syncer, bgpManager)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddress(),
		Handler:           web.New(cfg, db, syncer, bgpManager).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("HTTP listening on %s, BGP ASN %d port %d", cfg.ListenAddress(), cfg.LocalASN, cfg.BGPListenPort)
		serverErrors <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdownCtx)
}

func syncLoop(ctx context.Context, interval time.Duration, syncer *feeds.Syncer, manager *bgp.Manager) {
	syncNow := func() {
		for _, err := range syncer.SyncAll(ctx) {
			log.Printf("feed sync: %v", err)
		}
		if err := manager.Reconcile(ctx); err != nil && ctx.Err() == nil {
			log.Printf("BGP reconcile: %v", err)
		}
	}
	syncNow()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
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
	fmt.Printf("catalog: %d categories, %d services, %d entries\n", categories, services, entries)
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
		fmt.Printf("%s: %s\n", feed.Name, status)
	}
	return nil
}

func healthcheck(cfg config.Config) error {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}
