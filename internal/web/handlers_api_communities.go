package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/logging"
)

// errCommunityExportASNUnstable is returned when the ASN kept changing
// across every render attempt in buildCommunityExport's retry budget. It
// signals a transient condition (an unusually fast burst of BGP restarts or
// setting saves), not a permanent failure, so apiCommunitiesExport answers
// 503 rather than 500: a consumer polling this endpoint should just retry.
var errCommunityExportASNUnstable = errors.New("community export: asn did not stabilize")

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
		if errors.Is(err, errCommunityExportASNUnstable) {
			s.httpError(w, r, "error.asn_unstable", http.StatusServiceUnavailable)
			return
		}
		s.internalError(w, r, err)
		return
	}

	// Hash the document before stamping GeneratedAt: with the timestamp
	// included every response would be a new ETag and the 304 path — the
	// whole point for a consumer polling on a timer — could never hit.
	// Because of that, two responses sharing this ETag are not byte-for-byte
	// identical (generated_at differs) — only semantically equivalent, so
	// this must be a weak validator (RFC 7232 §2.1): a strong one asserts
	// exact byte equality, which caches and clients are entitled to rely on
	// (e.g. to satisfy a Range request from a cached copy without
	// re-fetching). Weak comparison is exactly what If-None-Match uses for
	// cache revalidation, so this changes nothing about the 304 behavior.
	payload, err := json.Marshal(doc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sum := sha256.Sum256(payload)
	etag := `W/"` + hex.EncodeToString(sum[:]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if ifNoneMatchHit(r.Header.Get("If-None-Match"), etag) {
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
	doc := communityExportDoc{
		SchemaVersion: communityExportSchemaVersion,
		Modes:         []communityExportMode{},
	}

	// Disabled modes are included: their communities still exist and a
	// consumer generating policy wants the full map, not one that shifts
	// when an operator toggles a mode.
	//
	// The mode list and every mode's catalog/communities/counts come from
	// one shared transaction (AllModeCommunitySnapshots), not one
	// transaction per mode: a feed sync's catalog update can span several
	// modes, and reading each mode independently could otherwise observe
	// the update committed for one mode but not yet for another, even
	// though the sync published both atomically.
	modes, snapshots, err := s.store.AllModeCommunitySnapshots(ctx, false)
	if err != nil {
		return communityExportDoc{}, fmt.Errorf("read community snapshots: %w", err)
	}

	for _, mode := range modes {
		snap := snapshots[mode.ID]

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

			// LargeCommunity is deliberately left blank here: it is filled
			// in below from an ASN read AFTER all database work finishes,
			// so an admin changing the active ASN mid-request can never
			// leave this response rendering every large_community string
			// from an ASN that stopped being accurate before the response
			// was even sent.
			entry := communityExportCategory{
				Name:          category,
				Community:     groupCommunities[category],
				PrefixCountV4: snap.CategoryPrefixV4[category],
				PrefixCountV6: snap.CategoryPrefixV6[category],
				Services:      make([]communityExportService, 0, len(services)),
			}
			for _, service := range services {
				entry.Services = append(entry.Services, communityExportService{
					Name:          service,
					Community:     serviceCommunities[category][service],
					PrefixCountV4: snap.ServicePrefixV4[category][service],
					PrefixCountV6: snap.ServicePrefixV6[category][service],
				})
			}
			exported.Categories = append(exported.Categories, entry)
		}
		doc.Modes = append(doc.Modes, exported)
	}

	// Read last, after every mode's data, and used only to render the wire
	// form of values already fixed above — never to decide which values are
	// exported. A BGP restart changing the active ASN while the database
	// reads above were still in flight would otherwise be rendered into
	// large_community strings using the pre-restart ASN, which stops being
	// accurate the moment the restart finishes — before this response is
	// even sent.
	//
	// Rendering itself takes a little time (one pass over every mode), so a
	// restart could in principle complete during that pass too. Unlike
	// correlating against Reconcile (an unrelated, asynchronously-batched
	// background process with no bound on when it next runs), ActiveASN is
	// a cheap in-memory read behind a mutex in this same process, and it
	// only ever changes on a full, synchronous BGP restart — an event far
	// rarer and slower than one render pass. So re-checking it after
	// rendering and redoing the pass if it moved actually converges, rather
	// than chasing an ever-receding window: bounded to a few attempts so a
	// pathological burst of restarts can't wedge the request, not because
	// convergence is expected to need them.
	const maxASNRenderAttempts = 5
	stable := false
	for attempt := 1; attempt <= maxASNRenderAttempts; attempt++ {
		activeASN, running := s.bgp.ActiveASN()
		configured := s.settings.LocalASN.Get()
		renderCommunityExportASN(&doc, activeASN, running, configured)

		// Both values are rechecked: LocalASN can change from an ordinary
		// settings save with no BGP reload at all, independently of
		// ActiveASN, so a render is only genuinely final once neither one
		// moved between the read it was rendered from and this recheck.
		recheckASN, recheckRunning := s.bgp.ActiveASN()
		recheckConfigured := s.settings.LocalASN.Get()
		if recheckASN == activeASN && recheckRunning == running && recheckConfigured == configured {
			stable = true
			break
		}
	}
	if !stable {
		// Every attempt in the budget still saw the ASN move between render
		// and recheck. Publishing the last attempt's document anyway would
		// mean knowingly returning a snapshot already known to be stale —
		// worse than telling the caller to retry.
		return communityExportDoc{}, errCommunityExportASNUnstable
	}

	return doc, nil
}

// renderCommunityExportASN stamps doc.ASN/BGPRunning/ASNConfigured and every
// category/service's LargeCommunity string from the given ASN snapshot.
// Factored out so buildCommunityExport can redo the render in place if a
// recheck finds the ASN moved mid-render, without re-reading any mode data.
func renderCommunityExportASN(doc *communityExportDoc, activeASN uint32, running bool, configured uint32) {
	doc.ASN = activeASN
	doc.BGPRunning = running
	doc.ASNConfigured = nil
	if !running {
		// Nothing is announced, so there is no wire value to report; the
		// configured ASN is what a restart would begin stamping.
		doc.ASN = configured
	} else if activeASN != configured {
		doc.ASNConfigured = &configured
	}
	for m := range doc.Modes {
		for c := range doc.Modes[m].Categories {
			category := &doc.Modes[m].Categories[c]
			category.LargeCommunity = largeCommunityString(doc.ASN, category.Community)
			for sv := range category.Services {
				service := &category.Services[sv]
				service.LargeCommunity = largeCommunityString(doc.ASN, service.Community)
			}
		}
	}
}

// ifNoneMatchHit reports whether an If-None-Match header matches etag, per
// RFC 7232 §2.3: a GET uses weak comparison (so a "W/" prefix on either side
// is ignored for the match), the header may carry a comma-separated list of
// validators, and "*" matches any current representation. Comparing the raw
// header to the single weak etag string directly — this handler's original
// approach — satisfies none of the three, and a conforming cache honoring
// any of them would get a full 200 on every poll instead of the 304 this
// endpoint's whole polling contract depends on.
func ifNoneMatchHit(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	target := strings.TrimPrefix(etag, "W/")
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == target {
			return true
		}
	}
	return false
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
