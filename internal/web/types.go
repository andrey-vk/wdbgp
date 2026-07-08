package web

import (
	"context"
	"io/fs"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andrey-vk/wdbgp/internal/feeds"
	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

type BGP interface {
	Reconcile(context.Context) error
	ReloadPeers(context.Context) error
	PeerStates(context.Context) (map[string]string, error)
	AddPeer(context.Context, store.User) error
	UpdatePeer(context.Context, store.User) error
	DeletePeer(context.Context, string, int64) error
	// Status reports whether a BGP speaker is currently running, and the
	// error from the last failed Start/ReloadPeers attempt (nil when
	// running is true).
	Status() (running bool, lastErr error)
}

type Server struct {
	settings     *settings.Settings
	store        *store.Store
	syncer       *feeds.Syncer
	bgp          BGP
	defaultLang  locale
	handler      http.Handler
	spaFS        fs.FS
	loginLimiter *rateLimiter
	adminLimiter *rateLimiter
	startTime    time.Time
	degraded     bool
	degradedInfo DegradedInfo

	// restartPending is set by OnChange callbacks on the BGP restart-only
	// settings, and cleared once a reload succeeds. atomic since it's
	// written from settings-change callbacks and read from HTTP handlers
	// concurrently.
	restartPending atomic.Bool

	// syncAllInFlight guards against overlapping manual sync-all runs —
	// apiFeedsSyncAll answers 409 while a previous run's goroutine is
	// still working. Individual feeds are serialized by the Syncer's
	// per-feed locks; this only dedupes the whole-catalog operation.
	syncAllInFlight atomic.Bool

	// appCtx and bg tie handler-spawned background work (the async 202
	// feed syncs) to the application lifecycle: appCtx cancellation
	// propagates into their contexts, and shutdown waits on bg so the
	// goroutines finish recording results before the store closes.
	// appCtx is nil unless SetAppContext was called (tests).
	appCtx context.Context
	bg     sync.WaitGroup
}

// DegradedInfo carries version mismatch details for the degraded-mode page.
type DegradedInfo struct {
	CurrentVersion int
	ServerVersion  int
	Reason         string // why degraded (e.g. "no backup found")
}
