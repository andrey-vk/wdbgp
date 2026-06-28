package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		s.httpError(w, r, "error.database_unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n")) //nolint:errcheck // health endpoint, best-effort
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if !s.statusAuthorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Get database stats
	categories, services, totalPrefixes, err := s.store.Stats(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// Get BGP peer status
	peerStates, err := s.bgp.PeerStates(ctx)
	if err != nil {
		logger := logging.FromContext(ctx)
		logger.Error("failed to read BGP peer states", "error", err)
		peerStates = map[string]string{}
	}

	// Count connected peers
	connectedPeers := 0
	for _, state := range peerStates {
		if state == "ESTABLISHED" {
			connectedPeers++
		}
	}

	// Get feed sync status
	feeds, err := s.store.Feeds(ctx, false)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	var feedStatus []map[string]any
	var successfulSyncs, failedSyncs int
	var lastSyncTime *time.Time

	for _, feed := range feeds {
		status := map[string]any{
			"name":    feed.Name,
			"enabled": feed.Enabled,
			"url":     feed.URL,
		}

		if feed.LastSuccess != "" {
			if t, err := time.Parse(time.RFC3339, feed.LastSuccess); err == nil {
				status["last_success"] = t
				if lastSyncTime == nil || t.After(*lastSyncTime) {
					lastSyncTime = &t
				}
				successfulSyncs++
			}
		}

		if feed.LastError != "" {
			status["last_error"] = feed.LastError
			failedSyncs++
		}

		feedStatus = append(feedStatus, status)
	}

	// Get version/build info (simple placeholder for now)
	// In a real deployment, this could be set via ldflags
	buildInfo := map[string]string{
		"version":    "dev",
		"go_version": "1.26",
	}

	// Prepare response
	response := map[string]any{
		"uptime":   time.Since(s.startTime).Seconds(),
		"prefixes": totalPrefixes,
		"database": map[string]any{
			"connected":      true, // health check already passed
			"categories":     categories,
			"services":       services,
			"total_prefixes": totalPrefixes,
		},
		"bgp": map[string]any{
			"total_peers":     len(peerStates),
			"connected_peers": connectedPeers,
			"peer_states":     peerStates,
		},
		"feeds": map[string]any{
			"total":            len(feeds),
			"enabled":          countEnabledFeeds(feeds),
			"successful_syncs": successfulSyncs,
			"failed_syncs":     failedSyncs,
			"last_sync":        lastSyncTime,
			"details":          feedStatus,
		},
		"build":     buildInfo,
		"timestamp": time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger := logging.FromContext(ctx)
		logger.Error("failed to encode status response", "error", err)
	}
}

// statusAuthorized checks whether the request is authorized to access /status.
// Authorization is granted by IP whitelist or Bearer token.
func (s *Server) statusAuthorized(r *http.Request) bool {
	s.mu.RLock()
	cidrs := s.statusCIDRs
	token := s.statusToken
	s.mu.RUnlock()

	if len(cidrs) > 0 {
		clientIP := s.clientIP(r)
		ip, err := netip.ParseAddr(clientIP)
		if err == nil {
			for _, prefix := range cidrs {
				if prefix.Contains(ip) {
					return true
				}
			}
		}
	}
	if token != "" {
		auth := r.Header.Get("Authorization")
		if strings.TrimPrefix(auth, "Bearer ") == token {
			return true
		}
	}
	return false
}

func (s *Server) httpError(w http.ResponseWriter, r *http.Request, key string, status int) {
	lang, _ := requestLocale(r, s.defaultLang)
	http.Error(w, translate(lang, key), status)
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	logger := logging.FromContext(r.Context())
	logger.Error("request failed", "error", err, "path", r.URL.Path, "method", r.Method)
	s.httpError(w, r, "error.internal", http.StatusInternalServerError)
}

// logAdminAction logs security-relevant admin actions
func (s *Server) logAdminAction(r *http.Request, action, details string) {
	clientIP := s.clientIP(r)
	userAgent := r.Header.Get("User-Agent")
	logger := logging.FromContext(r.Context())
	logger.Info("admin action",
		"ip", clientIP,
		"action", action,
		"details", details,
		"user_agent", userAgent,
		"path", r.URL.Path,
		"method", r.Method,
	)
}
