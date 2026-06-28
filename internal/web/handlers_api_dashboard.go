package web

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type dashboardUserSnapshotJSON struct {
	RecordedAt     string `json:"time"`
	UsersDisabled  int    `json:"disabled"`
	UsersConnected int    `json:"connected"`
	UsersTotal     int    `json:"total"`
}

type dashboardFeedSnapshotJSON struct {
	RecordedAt string        `json:"time"`
	Prefixes   map[int64]int `json:"prefixes"`
}

type dashboardBGPJSON struct {
	TotalPeers     int                 `json:"total_peers"`
	ConnectedPeers int                 `json:"connected_peers"`
	Peers          []dashboardPeerJSON `json:"peers"`
}

type dashboardPeerJSON struct {
	Name  string `json:"name"`
	IP    string `json:"ip"`
	ASN   uint32 `json:"asn"`
	State string `json:"state"`
}

type dashboardFeedJSON struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	LastError string `json:"last_error,omitempty"`
}

type dashboardJSON struct {
	Prefixes       int                         `json:"prefixes"`
	Categories     int                         `json:"categories"`
	Services       int                         `json:"services"`
	BGP            dashboardBGPJSON            `json:"bgp"`
	Feeds          map[string]any              `json:"feeds"`
	Users          map[string]int              `json:"users"`
	UserHistory    []dashboardUserSnapshotJSON `json:"user_history"`
	FeedHistory    []dashboardFeedSnapshotJSON `json:"feed_history"`
	Modes          []modeJSON                  `json:"modes"`
	Uptime         float64                     `json:"uptime_seconds"`
	MetricsEnabled bool                        `json:"metrics_enabled"`
}

// apiDashboard handles GET /api/admin/dashboard.
func (s *Server) apiDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Stats
	categories, services, totalPrefixes, _ := s.store.Stats(ctx) //nolint:errcheck // best-effort lookup for display

	// Users
	users, _ := s.store.Users(ctx, false) //nolint:errcheck // best-effort lookup for display
	totalUsers := len(users)
	disabledUsers := 0
	for _, u := range users {
		if !u.Enabled {
			disabledUsers++
		}
	}
	enabledUsers := totalUsers - disabledUsers

	// BGP
	peerStates, _ := s.bgp.PeerStates(ctx) //nolint:errcheck // best-effort lookup for display
	connectedPeers := 0
	var peers []dashboardPeerJSON
	for _, u := range users {
		key := fmt.Sprintf("%s:%d", u.PeerIP, u.PeerASN)
		state := peerStates[key]
		if state == "ESTABLISHED" {
			connectedPeers++
		}
		peers = append(peers, dashboardPeerJSON{
			Name: u.Name, IP: u.PeerIP, ASN: u.PeerASN, State: state,
		})
	}

	// Feeds
	feeds, _ := s.store.Feeds(ctx, false) //nolint:errcheck // best-effort lookup for display
	enabledFeeds := 0
	failedFeeds := 0
	var feedItems []dashboardFeedJSON
	for _, f := range feeds {
		if f.Enabled {
			enabledFeeds++
		}
		if f.LastError != "" {
			failedFeeds++
		}
		feedItems = append(feedItems, dashboardFeedJSON{
			Name: f.Name, Enabled: f.Enabled, LastError: f.LastError,
		})
	}

	// Modes
	modes, _ := s.store.CatalogModes(ctx, false) //nolint:errcheck // best-effort lookup for display
	feedCounts, _ := s.store.ModeFeedCounts(ctx) //nolint:errcheck // best-effort lookup for display
	modeItems := make([]modeJSON, len(modes))
	for i, m := range modes {
		modeItems[i] = modeJSON{ID: m.ID, Key: m.Key, Name: m.Name, Enabled: m.Enabled, FeedCount: feedCounts[m.ID]}
	}

	// History
	days := s.metricsHistoryDays
	if days <= 0 {
		days = 14
	}
	userSnapshots, _ := s.store.GetUserSnapshots(ctx, days) //nolint:errcheck // best-effort lookup for display
	userHistory := make([]dashboardUserSnapshotJSON, len(userSnapshots))
	for i, sn := range userSnapshots {
		userHistory[i] = dashboardUserSnapshotJSON{
			RecordedAt:     sn.RecordedAt.Format("2006-01-02T15:04:05Z"),
			UsersDisabled:  sn.UsersDisabled,
			UsersConnected: sn.UsersConnected,
			UsersTotal:     sn.UsersTotal,
		}
	}

	feedSnapshots, _ := s.store.GetFeedSnapshots(ctx, days) //nolint:errcheck // best-effort lookup for display
	feedHistory := make([]dashboardFeedSnapshotJSON, len(feedSnapshots))
	for i, sn := range feedSnapshots {
		feedHistory[i] = dashboardFeedSnapshotJSON{
			RecordedAt: sn.RecordedAt.Format("2006-01-02T15:04:05Z"),
			Prefixes:   sn.Prefixes,
		}
	}

	resp := dashboardJSON{
		Prefixes:   totalPrefixes,
		Categories: categories,
		Services:   services,
		BGP: dashboardBGPJSON{
			TotalPeers:     len(peerStates),
			ConnectedPeers: connectedPeers,
			Peers:          peers,
		},
		Feeds: map[string]any{
			"total": len(feeds), "enabled": enabledFeeds, "failed": failedFeeds, "items": feedItems,
		},
		Users:          map[string]int{"total": totalUsers, "enabled": enabledUsers, "disabled": disabledUsers},
		UserHistory:    userHistory,
		FeedHistory:    feedHistory,
		Modes:          modeItems,
		Uptime:         time.Since(s.startTime).Seconds(),
		MetricsEnabled: s.metricsEnabled,
	}

	writeJSON(w, http.StatusOK, resp)
}

// recordFeedSnapshot saves a feed prefix count snapshot, only when changed.
func (s *Server) recordFeedSnapshot(ctx context.Context) {
	if !s.metricsEnabled {
		return
	}
	feeds, err := s.store.Feeds(ctx, false)
	if err != nil {
		return
	}
	counts := make(map[int64]int)
	for _, f := range feeds {
		var count int
		if err := s.store.DB.QueryRowContext(ctx,
			"SELECT COUNT(DISTINCT cidr) FROM catalog_entries WHERE feed_id = ?", f.ID).Scan(&count); err == nil && count > 0 {
			counts[f.ID] = count
		}
	}
	_ = s.store.SaveFeedSnapshot(ctx, counts) //nolint:errcheck // best-effort snapshot recording
}
