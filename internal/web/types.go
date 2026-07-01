package web

import (
	"context"
	"io/fs"
	"net/http"
	"sync"
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
}

type Server struct {
	settings    *settings.Settings
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

	mu          sync.RWMutex
}

// DegradedInfo carries version mismatch details for the degraded-mode page.
type DegradedInfo struct {
	CurrentVersion int
	ServerVersion  int
	Reason         string // why degraded (e.g. "no backup found")
}
