package web

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/logging"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// apiConfigExport handles GET /api/admin/config/export: the whole admin
// configuration (users, global route filters, communities, and modes) as one
// JSON document, for backup, diffing between instances, or import elsewhere.
// See Store.ConfigSnapshot for what is and isn't in scope.
func (s *Server) apiConfigExport(w http.ResponseWriter, r *http.Request) {
	snap, err := s.store.ConfigSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to build config snapshot"})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// apiConfigDiff handles POST /api/admin/config/diff: the structural
// difference between any two configuration snapshots — two exports the admin
// already has, from this instance or another. It never touches the database.
func (s *Server) apiConfigDiff(w http.ResponseWriter, r *http.Request) {
	var body struct {
		A store.ConfigSnapshot `json:"a"`
		B store.ConfigSnapshot `json:"b"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": store.DiffConfigSnapshots(body.A, body.B)})
}

// apiConfigImportPreview handles POST /api/admin/config/import/preview: a
// dry-run diff of an uploaded snapshot against this instance's current
// configuration, plus the digest apiConfigImport requires. Read-only — it
// writes nothing.
func (s *Server) apiConfigImportPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Snapshot store.ConfigSnapshot `json:"snapshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	current, err := s.store.ConfigSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to build config snapshot"})
		return
	}
	digest, err := body.Snapshot.Digest()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to fingerprint the uploaded snapshot"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"diff":   store.DiffConfigSnapshots(current, body.Snapshot),
		"digest": digest,
	})
}

// apiConfigImport handles POST /api/admin/config/import: applies an uploaded
// snapshot. digest must match the one the preview reported for this exact
// snapshot — a caller that never previewed, or one applying a snapshot that
// changed since, is rejected rather than applied blindly.
//
// Import is additive only, by design (see Store.ApplyConfigSnapshot's own
// doc comment): a user or mode this instance already has, that the snapshot
// doesn't name, is left alone. Global route filters are the one piece of
// ConfigSnapshot that ApplyConfigSnapshot itself never touches — changing
// them has to go through the settings layer (internal/settings), which keeps
// its own in-memory cache, not through a direct database write that would
// leave that cache stale — so they are applied here, in their own
// transaction, only when the snapshot's filters actually differ from the
// current ones.
func (s *Server) apiConfigImport(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	var body struct {
		Snapshot store.ConfigSnapshot `json:"snapshot"`
		Digest   string               `json:"digest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	digest, err := body.Snapshot.Digest()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to fingerprint the uploaded snapshot"})
		return
	}
	if body.Digest == "" || body.Digest != digest {
		writeJSON(w, http.StatusConflict, apiResponse{OK: false,
			Error: "The snapshot does not match the digest from its preview; preview it again before applying"})
		return
	}

	meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "config.imported"}
	result, err := s.store.ApplyConfigSnapshot(r.Context(), body.Snapshot, meta)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	filtersApplied := false
	current, err := s.store.GlobalRouteFilters(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to read current global filters"})
		return
	}
	if !routeFiltersEqual(current, body.Snapshot.GlobalFilters) {
		if err := s.applyGlobalFiltersFromImport(r, body.Snapshot.GlobalFilters, current); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
			return
		}
		filtersApplied = true
	}

	if s.bgp != nil {
		if err := s.bgp.Reconcile(r.Context()); err != nil {
			logging.FromContext(r.Context()).Debug("bgp reconcile failed after config import", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result":                 result,
		"global_filters_applied": filtersApplied,
	})
}

func routeFiltersEqual(a, b store.RouteFilters) bool {
	return strings.Join(a.Allow, "\n") == strings.Join(b.Allow, "\n") && strings.Join(a.Deny, "\n") == strings.Join(b.Deny, "\n")
}

// applyGlobalFiltersFromImport sets filter_allow/filter_deny to match an
// imported snapshot, through the settings layer's own transaction-scoped
// setters (not a direct database write), so the in-memory cache those
// settings keep updates along with the row — the same requirement
// apiSettingsPut's filter handling documents. Audited under the same action
// the Settings page itself uses, so the admin audit log doesn't need a
// second action name for the same kind of change.
func (s *Server) applyGlobalFiltersFromImport(r *http.Request, newFilters, before store.RouteFilters) error {
	ctx := r.Context()
	var commits []func()
	txErr := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		commits = nil // attempt-local: Store.Transaction may retry
		_, commitAllow, err := s.settings.FilterAllow.SetTx(ctx, tx, strings.Join(newFilters.Allow, "\n"))
		if err != nil {
			return err
		}
		commits = append(commits, commitAllow)
		_, commitDeny, err := s.settings.FilterDeny.SetTx(ctx, tx, strings.Join(newFilters.Deny, "\n"))
		if err != nil {
			return err
		}
		commits = append(commits, commitDeny)
		meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "route_filters.global_updated"}
		return store.AuditEntryTx(ctx, tx, meta, "settings", "",
			store.BoundRouteFiltersForAudit(before), store.BoundRouteFiltersForAudit(newFilters), false)
	})
	if txErr != nil {
		return txErr
	}
	for _, commit := range commits {
		commit()
	}
	return nil
}
