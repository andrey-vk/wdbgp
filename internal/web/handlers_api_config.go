package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

// rejectUnsupportedConfigSchema writes a 400 and reports true if snap isn't
// this version's schema — e.g. {} (schema_version 0, every field its zero
// value), which would otherwise decode as a valid, digest-able document that
// clears every global filter and silently drops every mode-feed link it
// claims to carry. Only guards the import path (preview and apply): a pure
// apiConfigDiff comparison of two differently-versioned exports is read-only
// and has a legitimate use (seeing what a version bump would actually
// change), so it isn't restricted here.
func rejectUnsupportedConfigSchema(w http.ResponseWriter, snap store.ConfigSnapshot) bool {
	if snap.SchemaVersion == store.ConfigSnapshotSchemaVersion {
		return false
	}
	writeJSON(w, http.StatusBadRequest, apiResponse{OK: false,
		Error: fmt.Sprintf("Unsupported configuration schema version %d (this instance exports and imports version %d)", snap.SchemaVersion, store.ConfigSnapshotSchemaVersion)})
	return true
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
	if rejectUnsupportedConfigSchema(w, body.Snapshot) {
		return
	}
	current, err := s.store.ConfigSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to build config snapshot"})
		return
	}
	digest, err := store.ConfigImportDigest(current, body.Snapshot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to fingerprint the import"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"diff":   store.DiffConfigSnapshots(current, body.Snapshot),
		"digest": digest,
	})
}

// apiConfigImport handles POST /api/admin/config/import: applies an uploaded
// snapshot. digest must match store.ConfigImportDigest(current, snapshot)
// recomputed right now, against the live configuration as it is at apply
// time — not only the snapshot that was uploaded, so either the uploaded
// file changing, or the live target drifting under it (another admin's edit,
// between this preview and this apply), rejects the apply with 409 rather
// than silently overwriting whatever changed.
//
// Import is additive only, by design (see Store.ApplyConfigSnapshot's own
// doc comment): a user or mode this instance already has, that the snapshot
// doesn't name, is left alone. Global route filters are the one piece of
// ConfigSnapshot that ApplyConfigSnapshot itself never touches — changing
// them has to go through the settings layer (internal/settings), which keeps
// its own in-memory cache, not through a direct database write that would
// leave that cache stale — so they are applied here, in their own
// transaction, only when the snapshot's filters actually differ from the
// current ones, and validated before Store.ApplyConfigSnapshot runs so an
// invalid filter can never be reported as a failure after everything else in
// the snapshot already committed.
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
	if rejectUnsupportedConfigSchema(w, body.Snapshot) {
		return
	}

	current, err := s.store.ConfigSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to read the current configuration"})
		return
	}
	digest, err := store.ConfigImportDigest(current, body.Snapshot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to fingerprint the import"})
		return
	}
	if body.Digest == "" || body.Digest != digest {
		writeJSON(w, http.StatusConflict, apiResponse{OK: false,
			Error: "The configuration changed, or the uploaded file changed, since the preview; preview it again before applying"})
		return
	}

	if err := s.validateGlobalFilters(body.Snapshot.GlobalFilters); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}

	meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "config.imported"}
	result, err := s.store.ApplyConfigSnapshot(r.Context(), body.Snapshot, meta)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	filtersApplied := false
	if !routeFiltersEqual(current.GlobalFilters, body.Snapshot.GlobalFilters) {
		if err := s.applyGlobalFiltersFromImport(r, body.Snapshot.GlobalFilters, current.GlobalFilters); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
			return
		}
		filtersApplied = true
	}

	s.syncBGPAfterConfigImport(r, result.AffectedUserIDs)
	writeJSON(w, http.StatusOK, map[string]any{
		"result":                 result,
		"global_filters_applied": filtersApplied,
	})
}

// syncBGPAfterConfigImport reloads every user an import created or updated
// into the BGP manager's own peer cache, which Reconcile alone does not do —
// Reconcile only reconciles announced routes against peerConfigs it already
// has, so a new peer would never appear, a disabled one would stay
// configured, and a changed IP/ASN would keep its old session, until a
// restart or an unrelated single-user edit. Best-effort and logged, not
// failed: by this point Store.ApplyConfigSnapshot's own transaction has
// already committed durably, so a BGP-side hiccup here is the same kind of
// post-commit side effect Reconcile's own failure already is treated as.
func (s *Server) syncBGPAfterConfigImport(r *http.Request, affectedUserIDs []int64) {
	if s.bgp == nil {
		return
	}
	for _, uid := range affectedUserIDs {
		reloaded, err := s.store.User(r.Context(), uid)
		if err != nil {
			logging.FromContext(r.Context()).Debug("config import: reload user for bgp sync failed", "error", err, "user_id", uid)
			continue
		}
		if reloaded.Enabled {
			if err := s.bgp.UpdatePeer(r.Context(), reloaded); err != nil {
				logging.FromContext(r.Context()).Debug("config import: bgp update peer failed", "error", err, "user_id", uid)
			}
		} else if err := s.bgp.DeletePeer(r.Context(), reloaded.PeerIP, reloaded.ID); err != nil {
			logging.FromContext(r.Context()).Debug("config import: bgp delete peer failed", "error", err, "user_id", uid)
		}
	}
	if err := s.bgp.Reconcile(r.Context()); err != nil {
		logging.FromContext(r.Context()).Debug("bgp reconcile failed after config import", "error", err)
	}
}

func routeFiltersEqual(a, b store.RouteFilters) bool {
	return strings.Join(a.Allow, "\n") == strings.Join(b.Allow, "\n") && strings.Join(a.Deny, "\n") == strings.Join(b.Deny, "\n")
}

// validateGlobalFilters runs the same validation Setting.SetTx would, without
// persisting anything — so an invalid CIDR in an imported snapshot's global
// filters is caught before Store.ApplyConfigSnapshot commits the rest of the
// snapshot, not after, which would otherwise leave an import that reports
// failure having already durably applied every mode and user change.
func (s *Server) validateGlobalFilters(filters store.RouteFilters) error {
	if err := s.settings.FilterAllow.Validate(strings.Join(filters.Allow, "\n")); err != nil {
		return fmt.Errorf("invalid global allow filter: %w", err)
	}
	if err := s.settings.FilterDeny.Validate(strings.Join(filters.Deny, "\n")); err != nil {
		return fmt.Errorf("invalid global deny filter: %w", err)
	}
	return nil
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
