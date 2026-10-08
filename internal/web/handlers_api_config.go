package web

import (
	"database/sql"
	"encoding/json"
	"errors"
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

// decodeCompleteConfigSnapshot parses raw as a ConfigSnapshot, additionally
// requiring global_filters, modes, and users to actually be present in the
// document (and not JSON null) — matching schema_version alone isn't
// enough: {"schema_version":1} also decodes cleanly, with every other field
// at its zero value, and confirming its preview would clear every global
// filter. A real export always includes all three keys (ConfigSnapshot's
// JSON tags have no omitempty), even when a value is a genuinely empty [].
//
// The same gap exists one level deeper: {"global_filters":{},...} passes the
// top-level check above (global_filters itself is present and non-null) but
// still silently decodes to an empty allow/deny, and a confirmed import
// would clear both exactly as if they'd been named explicitly as []. So
// global_filters' own required allow/deny keys are checked the same way,
// rather than accepting any non-null JSON value there either.
func decodeCompleteConfigSnapshot(raw json.RawMessage) (store.ConfigSnapshot, error) {
	var presence struct {
		GlobalFilters json.RawMessage `json:"global_filters"`
		Modes         json.RawMessage `json:"modes"`
		Users         json.RawMessage `json:"users"`
	}
	if err := json.Unmarshal(raw, &presence); err != nil {
		return store.ConfigSnapshot{}, err
	}
	present := func(f json.RawMessage) bool { return len(f) > 0 && string(f) != "null" }
	if !present(presence.GlobalFilters) || !present(presence.Modes) || !present(presence.Users) {
		return store.ConfigSnapshot{}, errors.New("incomplete configuration document: missing global_filters, modes, or users")
	}
	var filterPresence struct {
		Allow json.RawMessage `json:"allow"`
		Deny  json.RawMessage `json:"deny"`
	}
	if err := json.Unmarshal(presence.GlobalFilters, &filterPresence); err != nil {
		return store.ConfigSnapshot{}, err
	}
	if !present(filterPresence.Allow) || !present(filterPresence.Deny) {
		return store.ConfigSnapshot{}, errors.New("incomplete configuration document: global_filters is missing allow or deny")
	}
	var snap store.ConfigSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return store.ConfigSnapshot{}, err
	}
	return snap, nil
}

// apiConfigImportPreview handles POST /api/admin/config/import/preview: a
// dry-run diff of an uploaded snapshot against this instance's current
// configuration, plus the digest apiConfigImport requires. Read-only — it
// writes nothing.
func (s *Server) apiConfigImportPreview(w http.ResponseWriter, r *http.Request) {
	var raw struct {
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	var body struct{ Snapshot store.ConfigSnapshot }
	snap, err := decodeCompleteConfigSnapshot(raw.Snapshot)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	body.Snapshot = snap
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
// snapshot. Store.ApplyConfigSnapshot itself recomputes
// store.ConfigImportDigest(current, snapshot) against the live
// configuration from inside its own transaction, immediately before writing
// anything, and refuses with ErrConfigImportStale (surfaced here as 409) if
// it doesn't match digest — either the uploaded file changed since whatever
// preview produced that digest, or the live target itself drifted under it
// (another admin's edit, landing anywhere up to the instant this apply
// starts writing), so neither can silently overwrite the other.
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
// apiConfigImportPreFiltersHook, if set, runs in apiConfigImport right after
// Store.ApplyConfigSnapshot commits but before the global-filters step —
// for a test simulating a concurrent edit to global filters landing in
// exactly that gap (see TestAPIConfigImportReportsPartialSuccessWhenFiltersDrift).
var apiConfigImportPreFiltersHook func()

func (s *Server) apiConfigImport(w http.ResponseWriter, r *http.Request) {
	extendWriteDeadline(w, r) // synchronous BGP reconcile can outlive WriteTimeout
	var raw struct {
		Snapshot json.RawMessage `json:"snapshot"`
		Digest   string          `json:"digest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}
	var body struct {
		Snapshot store.ConfigSnapshot
		Digest   string
	}
	snap, err := decodeCompleteConfigSnapshot(raw.Snapshot)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}
	body.Snapshot, body.Digest = snap, raw.Digest
	if rejectUnsupportedConfigSchema(w, body.Snapshot) {
		return
	}
	if body.Digest == "" {
		writeJSON(w, http.StatusConflict, apiResponse{OK: false, Error: "Preview this import first"})
		return
	}

	if err := s.validateGlobalFilters(body.Snapshot.GlobalFilters); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
		return
	}

	// previousFilters is read before ApplyConfigSnapshot, which never
	// touches filter_allow/filter_deny, so it's equally valid to read right
	// after — doing it first means a single GlobalRouteFilters call serves
	// both as "what applyGlobalFiltersFromImport is replacing" (below) and,
	// implicitly, confirms the settings tables are reachable before the
	// heavier ApplyConfigSnapshot transaction runs at all.
	previousFilters, err := s.store.GlobalRouteFilters(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Failed to read the current global filters"})
		return
	}

	meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "config.imported"}
	result, err := s.store.ApplyConfigSnapshot(r.Context(), body.Snapshot, body.Digest, meta)
	if err != nil {
		if errors.Is(err, store.ErrConfigImportStale) {
			writeJSON(w, http.StatusConflict, apiResponse{OK: false,
				Error: "The configuration changed, or the uploaded file changed, since the preview; preview it again before applying"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}

	if apiConfigImportPreFiltersHook != nil {
		apiConfigImportPreFiltersHook()
	}

	// By this point Store.ApplyConfigSnapshot has already committed every
	// mode and user change durably — a failure in this filters step from
	// here on is reported as a partial success (global_filters_applied:
	// false plus global_filters_error), never as an overall import failure.
	// Reporting 500/409 here instead would tell the admin the whole import
	// failed while the entity changes it actually made stay applied, and —
	// worse — would skip the BGP peer sync below entirely, leaving the
	// speaker's peer cache stale for changes that did commit.
	filtersApplied := false
	var filtersError string
	if !routeFiltersEqual(previousFilters, body.Snapshot.GlobalFilters) {
		if err := s.applyGlobalFiltersFromImport(r, body.Snapshot.GlobalFilters, previousFilters); err != nil {
			if errors.Is(err, store.ErrConfigImportStale) {
				filtersError = "Global filters changed since the preview; apply them separately via Settings"
			} else {
				filtersError = "Failed to apply global filters: " + err.Error()
			}
			logging.FromContext(r.Context()).Debug("config import: global filters apply failed after entities committed", "error", err)
		} else {
			filtersApplied = true
		}
	}

	s.syncBGPAfterConfigImport(r, result.AffectedUserIDs)
	resp := map[string]any{
		"result":                 result,
		"global_filters_applied": filtersApplied,
	}
	if filtersError != "" {
		resp["global_filters_error"] = filtersError
	}
	writeJSON(w, http.StatusOK, resp)
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
//
// expectedBefore is rechecked against the live value from inside this same
// transaction, immediately before writing — the caller read it earlier
// (before Store.ApplyConfigSnapshot even ran), and without this recheck a
// second admin's edit to the global filters landing in the gap between that
// read and this call would be silently overwritten despite this import's
// own digest check having already passed. Returns store.ErrConfigImportStale
// on a mismatch, the same sentinel ApplyConfigSnapshot itself uses, so the
// caller handles both with one error check.
func (s *Server) applyGlobalFiltersFromImport(r *http.Request, newFilters, expectedBefore store.RouteFilters) error {
	ctx := r.Context()
	var commits []func()
	txErr := s.store.Transaction(ctx, func(tx *sql.Tx) error {
		commits = nil // attempt-local: Store.Transaction may retry
		actualBefore, err := store.GlobalRouteFiltersTx(ctx, tx)
		if err != nil {
			return err
		}
		if !routeFiltersEqual(actualBefore, expectedBefore) {
			return store.ErrConfigImportStale
		}
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
			store.BoundRouteFiltersForAudit(actualBefore), store.BoundRouteFiltersForAudit(newFilters), false)
	})
	if txErr != nil {
		return txErr
	}
	for _, commit := range commits {
		commit()
	}
	return nil
}
