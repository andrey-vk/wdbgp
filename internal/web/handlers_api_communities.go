package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
)

// communityExportSchemaVersion identifies the document layout. Bump it only
// for a breaking change to the shape, so a consumer pinning a version can
// tell "I do not understand this document" apart from "the data changed".
const communityExportSchemaVersion = 1

type communityExportDoc struct {
	SchemaVersion int    `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	// ASN is the value actually stamped into announced communities. It comes
	// from the running speaker's start-time snapshot, not the LocalASN
	// setting, so a generated policy always matches what is on the wire.
	ASN uint32 `json:"asn"`
	// ASNConfigured is set only when the LocalASN setting has diverged from
	// ASN — i.e. a BGP restart is pending and the new ASN is not announced
	// yet. Without this a consumer could not tell the document is reporting
	// the older, still-live value on purpose.
	ASNConfigured *uint32               `json:"asn_configured,omitempty"`
	BGPRunning    bool                  `json:"bgp_running"`
	Modes         []communityExportMode `json:"modes"`
}

type communityExportMode struct {
	ModeID     int64                     `json:"mode_id"`
	ModeName   string                    `json:"mode_name"`
	Enabled    bool                      `json:"enabled"`
	Categories []communityExportCategory `json:"categories"`
}

type communityExportCategory struct {
	Name           string                   `json:"name"`
	Community      uint32                   `json:"community"`
	LargeCommunity string                   `json:"large_community"`
	PrefixCountV4  int                      `json:"prefix_count_v4"`
	PrefixCountV6  int                      `json:"prefix_count_v6"`
	Services       []communityExportService `json:"services"`
}

type communityExportService struct {
	Name           string `json:"name"`
	Community      uint32 `json:"community"`
	LargeCommunity string `json:"large_community"`
	PrefixCountV4  int    `json:"prefix_count_v4"`
	PrefixCountV6  int    `json:"prefix_count_v6"`
}

// apiCommunitiesExport handles GET /api/communities.
//
// Machine-readable projection of every mode's community map, for generating
// downstream router policies. Guarded by the same token/CIDR gate as /status
// rather than an admin session, so it can be polled from a script.
//
// Every mode is in one document on purpose: community numbers are assigned
// per mode, so the same category can hold different values in different
// modes. A flat community→name map cannot express that and would silently
// mislead whoever generates filters from it.
// An admin session is accepted too, so the admin UI can offer the document
// straight from the browser. It exposes nothing new: the same values are
// already readable through the per-mode communities endpoint.
func (s *Server) apiCommunitiesExport(w http.ResponseWriter, r *http.Request) {
	if !s.statusAuthorized(r) && !s.hasAdminSession(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	doc, err := s.buildCommunityExport(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	// Hash the document before stamping GeneratedAt: with the timestamp
	// included every response would be a new ETag and the 304 path — the
	// whole point for a consumer polling on a timer — could never hit.
	payload, err := json.Marshal(doc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sum := sha256.Sum256(payload)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	doc.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		logging.Error("failed to write community export", "error", err)
	}
}

func (s *Server) buildCommunityExport(r *http.Request) (communityExportDoc, error) {
	ctx := r.Context()

	activeASN, running := s.bgp.ActiveASN()
	configured := s.settings.LocalASN.Get()
	doc := communityExportDoc{
		SchemaVersion: communityExportSchemaVersion,
		ASN:           activeASN,
		BGPRunning:    running,
		Modes:         []communityExportMode{},
	}
	if !running {
		// Nothing is announced, so there is no wire value to report; the
		// configured ASN is what a restart would begin stamping.
		doc.ASN = configured
	} else if activeASN != configured {
		doc.ASNConfigured = &configured
	}

	// Disabled modes are included: their communities still exist and a
	// consumer generating policy wants the full map, not one that shifts
	// when an operator toggles a mode.
	modes, err := s.store.CatalogModes(ctx, false)
	if err != nil {
		return communityExportDoc{}, fmt.Errorf("load modes: %w", err)
	}

	for _, mode := range modes {
		// A feed sync publishes its catalog and generates communities for it
		// as two separate transactions (internal/feeds/feeds.go), so reading
		// the catalog, assignments, and counts as separate queries — even
		// with a defensive generate first — leaves a window where a sync's
		// commit lands between this read's own generate step and its later
		// queries, and an entry still comes back with no assignment.
		// ModeCommunitySnapshot does the generate-then-read entirely inside
		// one transaction, which is what actually closes the window.
		snap, err := s.store.ModeCommunitySnapshot(ctx, mode.ID)
		if err != nil {
			return communityExportDoc{}, fmt.Errorf("read community snapshot for mode %d: %w", mode.ID, err)
		}

		// Structured rows, not GetCommunities' "category|service"-keyed map:
		// a category legitimately containing "|" would collide with that key
		// scheme (e.g. category "a" service "b" vs. group "a|b").
		groupCommunities := make(map[string]uint32, len(snap.Communities))
		serviceCommunities := make(map[string]map[string]uint32, len(snap.Communities))
		for _, row := range snap.Communities {
			if row.Service == "" {
				groupCommunities[row.Category] = row.Community
				continue
			}
			if serviceCommunities[row.Category] == nil {
				serviceCommunities[row.Category] = make(map[string]uint32)
			}
			serviceCommunities[row.Category][row.Service] = row.Community
		}

		categories := make([]string, 0, len(snap.Catalog))
		for category := range snap.Catalog {
			categories = append(categories, category)
		}
		sort.Strings(categories)

		exported := communityExportMode{
			ModeID:     mode.ID,
			ModeName:   mode.Name,
			Enabled:    mode.Enabled,
			Categories: make([]communityExportCategory, 0, len(categories)),
		}
		for _, category := range categories {
			services := make([]string, len(snap.Catalog[category]))
			copy(services, snap.Catalog[category])
			sort.Strings(services)

			groupValue := groupCommunities[category]
			entry := communityExportCategory{
				Name:           category,
				Community:      groupValue,
				LargeCommunity: largeCommunityString(doc.ASN, groupValue),
				PrefixCountV4:  snap.CategoryPrefixV4[category],
				PrefixCountV6:  snap.CategoryPrefixV6[category],
				Services:       make([]communityExportService, 0, len(services)),
			}
			for _, service := range services {
				value := serviceCommunities[category][service]
				entry.Services = append(entry.Services, communityExportService{
					Name:           service,
					Community:      value,
					LargeCommunity: largeCommunityString(doc.ASN, value),
					PrefixCountV4:  snap.ServicePrefixV4[category][service],
					PrefixCountV6:  snap.ServicePrefixV6[category][service],
				})
			}
			exported.Categories = append(exported.Categories, entry)
		}
		doc.Modes = append(doc.Modes, exported)
	}
	return doc, nil
}

// largeCommunityString renders the wire form of a catalog community, matching
// what buildRoute stamps onto a route: <asn>:0:<value>. An unassigned value
// (0) renders empty rather than a syntactically valid but meaningless
// community that a consumer might paste into a filter.
func largeCommunityString(asn, value uint32) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%d:0:%d", asn, value)
}
