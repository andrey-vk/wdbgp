package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/settings"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// splitFilterLines splits a filter_allow/filter_deny raw stored value (one
// entry per line, stored verbatim — see validateFilterList) into a slice
// for diffing, dropping blank lines. Comments are kept as literal entries:
// they're real content an admin directly edited, and a diff should still
// surface a comment-only change rather than silently hiding it.
func splitFilterLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// apiSettingsGet handles GET /api/admin/settings.
func (s *Server) apiSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings.JSON(r.Context()))
}

// filterKeysAffectBGP are the setting keys that change what routes get
// announced. Unlike the restart-only BGP identity settings (ASN, router ID,
// etc.), these take effect immediately — reconcileLocked() just needs to
// run again with the new value already persisted, no speaker restart.
var filterKeysAffectBGP = map[string]bool{"filter_allow": true, "filter_deny": true}

// settingsPutFilterApplyHook, if set, runs once per apiSettingsPut call,
// inside the globalFilterMu-guarded section right after beforeFilters is
// captured — a test seam used to force two concurrent calls to overlap
// deterministically and verify the lock actually serializes them.
var settingsPutFilterApplyHook func()

// settingsPutFilterTxPreCommitHook, if set, runs once per filter-settings
// transaction, after both SetTx/ResetTx calls but before the audit insert
// and commit — a test seam used to force the transaction to fail right
// before it would otherwise commit, so a test can verify the already-
// applied SetTx/ResetTx writes roll back together with the audit insert
// rather than leaving the setting persisted with no audit trace.
var settingsPutFilterTxPreCommitHook func() error

// apiSettingsPut handles PUT /api/admin/settings.
func (s *Server) apiSettingsPut(w http.ResponseWriter, r *http.Request) {
	extendRequestDeadlines(w, r) // large filter upload + reconcile can outlive Read/WriteTimeout
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	ctx := r.Context()

	// Validate every key before applying any of them. Without this, a
	// request with one valid and one invalid field could persist the valid
	// change while still returning 400 — and since Go map iteration order is
	// randomized, which fields "stick" would be nondeterministic across
	// requests.
	for key, raw := range body {
		if err := s.validateSettingKey(key, raw); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
			return
		}
	}

	reconcileNeeded := false
	// The filter_allow/filter_deny before-capture, apply, and after-capture
	// (for the audit entry below) run under globalFilterMu so two
	// overlapping PUTs can't interleave — without it, both could capture
	// the same "before" and each report its own "after", turning a real
	// A->B->C sequence into misattributed or duplicated audit rows. The
	// lock is released before Reconcile, which doesn't need to be
	// serialized by it and can be comparatively slow.
	applyErr := func() error {
		s.globalFilterMu.Lock()
		defer s.globalFilterMu.Unlock()

		beforeFilters := map[string]string{
			"filter_allow": s.settings.FilterAllow.Get(),
			"filter_deny":  s.settings.FilterDeny.Get(),
		}
		if settingsPutFilterApplyHook != nil {
			settingsPutFilterApplyHook()
		}

		// Non-filter keys: applied via the existing non-tx path — they
		// aren't audited and don't need the atomicity filter_allow/
		// filter_deny get below.
		for key, raw := range body {
			if filterKeysAffectBGP[key] {
				continue
			}
			if string(raw) == "null" {
				if err := s.resetSetting(ctx, key); err != nil {
					return err
				}
			} else if err := s.setSetting(ctx, key, raw); err != nil {
				return err
			}
		}

		// filter_allow/filter_deny: persisted and audited in ONE
		// transaction, so the audit insert commits atomically with the
		// setting write and lands in audit_log in true commit order
		// relative to every other audited mutation — unlike every other
		// hook (folded into its own mutation's transaction already), this
		// one had no transaction of its own to fold into before
		// SetTx/ResetTx existed. The in-memory cached value and OnChange
		// callbacks (the commit funcs collected below) only run after
		// this transaction actually commits.
		_, filterAllowProvided := body["filter_allow"]
		_, filterDenyProvided := body["filter_deny"]
		if !filterAllowProvided && !filterDenyProvided {
			return nil
		}
		reconcileNeeded = true

		if s.store == nil {
			// No audit-log store available (some tests construct a
			// *Server without one, for handlers that otherwise don't
			// need it) — apply filter keys the same way as any other
			// key, with no tx/audit wrapping; there's no audit_log table
			// to write to anyway.
			for _, key := range [2]string{"filter_allow", "filter_deny"} {
				raw, ok := body[key]
				if !ok {
					continue
				}
				if string(raw) == "null" {
					if err := s.resetSetting(ctx, key); err != nil {
						return err
					}
				} else if err := s.setSetting(ctx, key, raw); err != nil {
					return err
				}
			}
			return nil
		}

		var commits []func()
		txErr := s.store.Transaction(ctx, func(tx *sql.Tx) error {
			commits = nil // attempt-local: Store.Transaction may retry
			after := map[string]string{
				"filter_allow": beforeFilters["filter_allow"],
				"filter_deny":  beforeFilters["filter_deny"],
			}
			apply := func(st settings.Setting[string, string], key string) error {
				raw, ok := body[key]
				if !ok {
					return nil
				}
				var v string
				var commit func()
				var err error
				if string(raw) == "null" {
					v, commit, err = st.ResetTx(ctx, tx)
				} else {
					v, commit, err = callStringSettingTx(ctx, tx, st, raw)
				}
				if err != nil {
					return err
				}
				after[key] = v
				commits = append(commits, commit)
				return nil
			}
			if err := apply(s.settings.FilterAllow, "filter_allow"); err != nil {
				return err
			}
			if err := apply(s.settings.FilterDeny, "filter_deny"); err != nil {
				return err
			}
			if settingsPutFilterTxPreCommitHook != nil {
				if err := settingsPutFilterTxPreCommitHook(); err != nil {
					return err
				}
			}

			// Bounded to just the lines that changed — the full text has
			// no size limit (filter_allow/filter_deny accept arbitrarily
			// large lists), so logging it whole on every edit could make
			// a single audit row, or a page of them, arbitrarily large.
			removedAllow, addedAllow := store.DiffStringSet(splitFilterLines(beforeFilters["filter_allow"]), splitFilterLines(after["filter_allow"]))
			removedDeny, addedDeny := store.DiffStringSet(splitFilterLines(beforeFilters["filter_deny"]), splitFilterLines(after["filter_deny"]))
			// Hard-capped (not just diffed): replacing an entire large
			// filter_allow/filter_deny text with a disjoint large one
			// would otherwise still put every old line in "removed" and
			// every new line in "added", unbounded by the diff alone.
			removed := map[string]store.AuditStringList{
				"filter_allow": store.BoundStringListForAudit(removedAllow),
				"filter_deny":  store.BoundStringListForAudit(removedDeny),
			}
			added := map[string]store.AuditStringList{
				"filter_allow": store.BoundStringListForAudit(addedAllow),
				"filter_deny":  store.BoundStringListForAudit(addedDeny),
			}
			meta := store.AuditMeta{Actor: s.adminActor(r), UserAgent: r.Header.Get("User-Agent"), Action: "route_filters.global_updated"}
			return store.AuditEntryTx(ctx, tx, meta, "settings", "", removed, added, false)
		})
		if txErr != nil {
			return txErr
		}
		for _, commit := range commits {
			commit()
		}
		return nil
	}()
	if applyErr != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: applyErr.Error()})
		return
	}

	// Global route filters change what DesiredPrefixes() computes, so a
	// change here must apply now — otherwise already-announced prefixes
	// keep using the old filter set until the next scheduled sync/reconcile
	// (up to sync_interval seconds later), which for a deny filter means
	// routes the admin just tried to block stay live in the meantime.
	//
	// By this point every changed setting is already persisted — Reconcile
	// failing doesn't undo that, and can't safely be made to (a partial
	// rollback across the generic Setting[T] framework has its own failure
	// modes). Report success with a warning rather than a 500 "saved but
	// failed", so the client doesn't have to guess whether the write stuck.
	if reconcileNeeded && s.bgp != nil {
		if err := s.bgp.Reconcile(ctx); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":      true,
				"warning": "Settings saved, but BGP reconciliation failed: " + err.Error(),
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// setSetting parses raw JSON and calls the typed Set method on the setting.
func (s *Server) setSetting(ctx context.Context, key string, raw json.RawMessage) error {
	switch key {
	case "active_dial":
		return callBoolSetting(ctx, s.settings.ActiveDial, raw)
	case "adapter_backup_dir":
		return callStringSetting(ctx, s.settings.AdapterBackupDir, raw)
	case "adapter_backup_max":
		return callIntSetting(ctx, s.settings.AdapterBackupMax, raw)
	case "admin_cookie_secure":
		return callStringSetting(ctx, s.settings.AdminCookieSecure, raw)
	case "alert_prefix_baseline_minimum":
		return callIntSetting(ctx, s.settings.AlertPrefixBaselineMinimum, raw)
	case "alert_prefix_drop_threshold_percent":
		return callIntSetting(ctx, s.settings.AlertPrefixDropThresholdPercent, raw)
	case "alert_webhook_url":
		return callStringSetting(ctx, s.settings.AlertWebhookURL, raw)
	case "allow_dynamic_peers":
		return callBoolSetting(ctx, s.settings.AllowDynamicPeers, raw)
	case "audit_log_retention_days":
		return callIntSetting(ctx, s.settings.AuditLogRetentionDays, raw)
	case "auto_restore_enabled":
		return fmt.Errorf("auto_restore_enabled is set via WDBGP_AUTO_RESTORE_ENABLED and cannot be changed here")
	case "bgp_hold_time":
		return callUint16Setting(ctx, s.settings.BGPHoldTime, raw)
	case "bgp_port":
		return callUint16Setting(ctx, s.settings.BGPPort, raw)
	case "backup_dir":
		return fmt.Errorf("backup_dir is set via WDBGP_BACKUP_DIR and cannot be changed here")
	case "backup_enabled":
		return fmt.Errorf("backup_enabled is set via WDBGP_BACKUP_ENABLED and cannot be changed here")
	case "default_language":
		return callStringSetting(ctx, s.settings.DefaultLanguage, raw)
	case "default_web_auth":
		return callStringSetting(ctx, s.settings.DefaultWebAuth, raw)
	case "dynamic_peer_md5_match":
		return callBoolSetting(ctx, s.settings.DynamicPeerMD5Match, raw)
	case "dynamic_peer_md5_queue_num":
		return callUint16Setting(ctx, s.settings.DynamicPeerMD5QueueNum, raw)
	case "filter_allow":
		return callStringSetting(ctx, s.settings.FilterAllow, raw)
	case "filter_deny":
		return callStringSetting(ctx, s.settings.FilterDeny, raw)
	case "host":
		return fmt.Errorf("host is set via WDBGP_HOST and cannot be changed here")
	case "js_max_call_stack":
		return callIntSetting(ctx, s.settings.JSMaxCallStack, raw)
	case "js_max_entries":
		return callIntSetting(ctx, s.settings.JSMaxEntries, raw)
	case "js_max_requests":
		return callIntSetting(ctx, s.settings.JSMaxRequests, raw)
	case "js_max_response":
		return callIntSetting(ctx, s.settings.JSMaxResponseBytes, raw)
	case "js_max_source":
		return callIntSetting(ctx, s.settings.JSMaxSourceBytes, raw)
	case "js_max_total":
		return callIntSetting(ctx, s.settings.JSMaxTotalBytes, raw)
	case "js_timeout":
		return callIntSetting(ctx, s.settings.JSTimeout, raw)
	case "local_asn":
		return callUint32Setting(ctx, s.settings.LocalASN, raw)
	case "local_address_v4":
		return callStringSetting(ctx, s.settings.LocalAddressV4, raw)
	case "local_address_v6":
		return callStringSetting(ctx, s.settings.LocalAddressV6, raw)
	case "log_format":
		return callStringSetting(ctx, s.settings.LogFormat, raw)
	case "log_level":
		return callStringSetting(ctx, s.settings.LogLevel, raw)
	case "metrics_enabled":
		return callBoolSetting(ctx, s.settings.MetricsEnabled, raw)
	case "metrics_history_days":
		return callIntSetting(ctx, s.settings.MetricsHistoryDays, raw)
	case "port":
		return fmt.Errorf("port is set via WDBGP_PORT and cannot be changed here")
	case "rate_limit_admin":
		return callIntSetting(ctx, s.settings.RateLimitAdmin, raw)
	case "rate_limit_login":
		return callIntSetting(ctx, s.settings.RateLimitLogin, raw)
	case "router_id":
		return callStringSetting(ctx, s.settings.RouterID, raw)
	case "security_headers":
		return callBoolSetting(ctx, s.settings.SecurityHeaders, raw)
	case "session_max_age":
		return callIntSetting(ctx, s.settings.SessionMaxAge, raw)
	case "status_allowed":
		return callStringSetting(ctx, s.settings.StatusAllowed, raw)
	case "status_token":
		return callStringSetting(ctx, s.settings.StatusToken, raw)
	case "sync_interval":
		return callIntSetting(ctx, s.settings.SyncInterval, raw)
	case "trust_proxy_headers":
		return callBoolSetting(ctx, s.settings.TrustProxyHeaders, raw)
	case "require_password_for_non_unique_ip":
		return callBoolSetting(ctx, s.settings.RequirePasswordForNonUniqueIP, raw)
	case "db_path":
		return fmt.Errorf("db_path is set via WDBGP_DB and cannot be changed here")
	case "admin_password":
		return callStringSetting(ctx, s.settings.AdminPassword, raw)
	case "session_secret":
		return callStringSetting(ctx, s.settings.SessionSecret, raw)
	}
	return fmt.Errorf("unknown setting: %s", key)
}

// validateSettingKey reports whether setSetting/resetSetting would accept
// this key/value, without calling either — mirrors setSetting's case list
// exactly, but dispatches to Validate instead of Set, and skips validation
// entirely for a reset ("null") value, since resetting to a pre-validated
// default can never fail on value grounds (only the readonly-blocked keys
// below can still reject a reset).
func (s *Server) validateSettingKey(key string, raw json.RawMessage) error {
	isReset := string(raw) == "null"
	switch key {
	case "active_dial":
		if isReset {
			return nil
		}
		return callBoolValidate(s.settings.ActiveDial, raw)
	case "adapter_backup_dir":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.AdapterBackupDir, raw)
	case "adapter_backup_max":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.AdapterBackupMax, raw)
	case "admin_cookie_secure":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.AdminCookieSecure, raw)
	case "alert_prefix_baseline_minimum":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.AlertPrefixBaselineMinimum, raw)
	case "alert_prefix_drop_threshold_percent":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.AlertPrefixDropThresholdPercent, raw)
	case "alert_webhook_url":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.AlertWebhookURL, raw)
	case "allow_dynamic_peers":
		if isReset {
			return nil
		}
		return callBoolValidate(s.settings.AllowDynamicPeers, raw)
	case "audit_log_retention_days":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.AuditLogRetentionDays, raw)
	case "auto_restore_enabled":
		return fmt.Errorf("auto_restore_enabled is set via WDBGP_AUTO_RESTORE_ENABLED and cannot be changed here")
	case "bgp_hold_time":
		if isReset {
			return nil
		}
		return callUint16Validate(s.settings.BGPHoldTime, raw)
	case "bgp_port":
		if isReset {
			return nil
		}
		return callUint16Validate(s.settings.BGPPort, raw)
	case "backup_dir":
		return fmt.Errorf("backup_dir is set via WDBGP_BACKUP_DIR and cannot be changed here")
	case "backup_enabled":
		return fmt.Errorf("backup_enabled is set via WDBGP_BACKUP_ENABLED and cannot be changed here")
	case "default_language":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.DefaultLanguage, raw)
	case "default_web_auth":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.DefaultWebAuth, raw)
	case "dynamic_peer_md5_match":
		if isReset {
			return nil
		}
		return callBoolValidate(s.settings.DynamicPeerMD5Match, raw)
	case "dynamic_peer_md5_queue_num":
		if isReset {
			return nil
		}
		return callUint16Validate(s.settings.DynamicPeerMD5QueueNum, raw)
	case "filter_allow":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.FilterAllow, raw)
	case "filter_deny":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.FilterDeny, raw)
	case "host":
		return fmt.Errorf("host is set via WDBGP_HOST and cannot be changed here")
	case "js_max_call_stack":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.JSMaxCallStack, raw)
	case "js_max_entries":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.JSMaxEntries, raw)
	case "js_max_requests":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.JSMaxRequests, raw)
	case "js_max_response":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.JSMaxResponseBytes, raw)
	case "js_max_source":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.JSMaxSourceBytes, raw)
	case "js_max_total":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.JSMaxTotalBytes, raw)
	case "js_timeout":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.JSTimeout, raw)
	case "local_asn":
		if isReset {
			return nil
		}
		return callUint32Validate(s.settings.LocalASN, raw)
	case "local_address_v4":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.LocalAddressV4, raw)
	case "local_address_v6":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.LocalAddressV6, raw)
	case "log_format":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.LogFormat, raw)
	case "log_level":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.LogLevel, raw)
	case "metrics_enabled":
		if isReset {
			return nil
		}
		return callBoolValidate(s.settings.MetricsEnabled, raw)
	case "metrics_history_days":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.MetricsHistoryDays, raw)
	case "port":
		return fmt.Errorf("port is set via WDBGP_PORT and cannot be changed here")
	case "rate_limit_admin":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.RateLimitAdmin, raw)
	case "rate_limit_login":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.RateLimitLogin, raw)
	case "router_id":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.RouterID, raw)
	case "security_headers":
		if isReset {
			return nil
		}
		return callBoolValidate(s.settings.SecurityHeaders, raw)
	case "session_max_age":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.SessionMaxAge, raw)
	case "status_allowed":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.StatusAllowed, raw)
	case "status_token":
		if isReset {
			return nil
		}
		return callStringValidate(s.settings.StatusToken, raw)
	case "sync_interval":
		if isReset {
			return nil
		}
		return callIntValidate(s.settings.SyncInterval, raw)
	case "trust_proxy_headers":
		if isReset {
			return nil
		}
		return callBoolValidate(s.settings.TrustProxyHeaders, raw)
	case "require_password_for_non_unique_ip":
		if isReset {
			return nil
		}
		return callBoolValidate(s.settings.RequirePasswordForNonUniqueIP, raw)
	case "db_path":
		return fmt.Errorf("db_path is set via WDBGP_DB and cannot be changed here")
	case "admin_password":
		return validateNonEmptySecret(raw, isReset, "admin_password", s.settings.AdminPassword)
	case "session_secret":
		return validateNonEmptySecret(raw, isReset, "session_secret", s.settings.SessionSecret)
	}
	return fmt.Errorf("unknown setting: %s", key)
}

// validateNonEmptySecret rejects an empty value for the admin_password/
// session_secret settings. Their underlying Setting has no validate hook of
// its own: newSimple also runs validate against the zero-value default at
// construction time, and a fresh install with neither the secret nor its env
// var set must still be able to start — main.go's serve() already refuses to
// actually serve requests until both are non-empty, so construction-time
// enforcement would be redundant and would also wrongly block commands like
// migrate/sync that construct Settings but don't need admin auth at all.
// An empty value is only a problem once something is running and the API is
// used to set (or reset, which reverts to the same empty default) one of
// these at runtime: an empty admin_password lets any login attempt with an
// empty password succeed (hmac.Equal against ""), and an empty session_secret
// signs sessions with an empty key. So the check lives here instead, scoped
// to requests that explicitly try to change the value while serving.
func validateNonEmptySecret(raw json.RawMessage, isReset bool, name string, st settings.Setting[string, string]) error {
	if isReset {
		return fmt.Errorf("%s cannot be reset via the settings API; set its environment variable and restart instead", name)
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid string: %w", err)
	}
	if v == "" {
		return fmt.Errorf("%s cannot be empty", name)
	}
	return st.Validate(v)
}

// resetSetting calls Reset on the typed setting.
func (s *Server) resetSetting(ctx context.Context, key string) error {
	switch key {
	case "active_dial":
		return s.settings.ActiveDial.Reset(ctx)
	case "adapter_backup_dir":
		return s.settings.AdapterBackupDir.Reset(ctx)
	case "adapter_backup_max":
		return s.settings.AdapterBackupMax.Reset(ctx)
	case "admin_cookie_secure":
		return s.settings.AdminCookieSecure.Reset(ctx)
	case "alert_prefix_baseline_minimum":
		return s.settings.AlertPrefixBaselineMinimum.Reset(ctx)
	case "alert_prefix_drop_threshold_percent":
		return s.settings.AlertPrefixDropThresholdPercent.Reset(ctx)
	case "alert_webhook_url":
		return s.settings.AlertWebhookURL.Reset(ctx)
	case "allow_dynamic_peers":
		return s.settings.AllowDynamicPeers.Reset(ctx)
	case "audit_log_retention_days":
		return s.settings.AuditLogRetentionDays.Reset(ctx)
	case "auto_restore_enabled":
		return fmt.Errorf("auto_restore_enabled is set via WDBGP_AUTO_RESTORE_ENABLED and cannot be changed here")
	case "bgp_hold_time":
		return s.settings.BGPHoldTime.Reset(ctx)
	case "bgp_port":
		return s.settings.BGPPort.Reset(ctx)
	case "backup_dir":
		return fmt.Errorf("backup_dir is set via WDBGP_BACKUP_DIR and cannot be changed here")
	case "backup_enabled":
		return fmt.Errorf("backup_enabled is set via WDBGP_BACKUP_ENABLED and cannot be changed here")
	case "default_language":
		return s.settings.DefaultLanguage.Reset(ctx)
	case "default_web_auth":
		return s.settings.DefaultWebAuth.Reset(ctx)
	case "dynamic_peer_md5_match":
		return s.settings.DynamicPeerMD5Match.Reset(ctx)
	case "dynamic_peer_md5_queue_num":
		return s.settings.DynamicPeerMD5QueueNum.Reset(ctx)
	case "filter_allow":
		return s.settings.FilterAllow.Reset(ctx)
	case "filter_deny":
		return s.settings.FilterDeny.Reset(ctx)
	case "host":
		return fmt.Errorf("host is set via WDBGP_HOST and cannot be changed here")
	case "js_max_call_stack":
		return s.settings.JSMaxCallStack.Reset(ctx)
	case "js_max_entries":
		return s.settings.JSMaxEntries.Reset(ctx)
	case "js_max_requests":
		return s.settings.JSMaxRequests.Reset(ctx)
	case "js_max_response":
		return s.settings.JSMaxResponseBytes.Reset(ctx)
	case "js_max_source":
		return s.settings.JSMaxSourceBytes.Reset(ctx)
	case "js_max_total":
		return s.settings.JSMaxTotalBytes.Reset(ctx)
	case "js_timeout":
		return s.settings.JSTimeout.Reset(ctx)
	case "local_asn":
		return s.settings.LocalASN.Reset(ctx)
	case "local_address_v4":
		return s.settings.LocalAddressV4.Reset(ctx)
	case "local_address_v6":
		return s.settings.LocalAddressV6.Reset(ctx)
	case "log_format":
		return s.settings.LogFormat.Reset(ctx)
	case "log_level":
		return s.settings.LogLevel.Reset(ctx)
	case "metrics_enabled":
		return s.settings.MetricsEnabled.Reset(ctx)
	case "metrics_history_days":
		return s.settings.MetricsHistoryDays.Reset(ctx)
	case "port":
		return fmt.Errorf("port is set via WDBGP_PORT and cannot be changed here")
	case "rate_limit_admin":
		return s.settings.RateLimitAdmin.Reset(ctx)
	case "rate_limit_login":
		return s.settings.RateLimitLogin.Reset(ctx)
	case "router_id":
		return s.settings.RouterID.Reset(ctx)
	case "security_headers":
		return s.settings.SecurityHeaders.Reset(ctx)
	case "session_max_age":
		return s.settings.SessionMaxAge.Reset(ctx)
	case "status_allowed":
		return s.settings.StatusAllowed.Reset(ctx)
	case "status_token":
		return s.settings.StatusToken.Reset(ctx)
	case "sync_interval":
		return s.settings.SyncInterval.Reset(ctx)
	case "trust_proxy_headers":
		return s.settings.TrustProxyHeaders.Reset(ctx)
	case "require_password_for_non_unique_ip":
		return s.settings.RequirePasswordForNonUniqueIP.Reset(ctx)
	case "db_path":
		return fmt.Errorf("db_path is set via WDBGP_DB and cannot be changed here")
	case "admin_password":
		return fmt.Errorf("admin_password cannot be reset via the settings API; set WDBGP_ADMIN_PASSWORD and restart instead")
	case "session_secret":
		return fmt.Errorf("session_secret cannot be reset via the settings API; set WDBGP_SESSION_SECRET and restart instead")
	}
	return fmt.Errorf("unknown setting: %s", key)
}

func callBoolSetting(ctx context.Context, st settings.Setting[bool, bool], raw json.RawMessage) error {
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid bool: %w", err)
	}
	return st.Set(ctx, v)
}

func callIntSetting(ctx context.Context, st settings.Setting[int, int], raw json.RawMessage) error {
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid int: %w", err)
	}
	return st.Set(ctx, v)
}

func callStringSetting(ctx context.Context, st settings.Setting[string, string], raw json.RawMessage) error {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid string: %w", err)
	}
	return st.Set(ctx, v)
}

// callStringSettingTx is callStringSetting's tx-scoped counterpart — see
// Setting.SetTx.
func callStringSettingTx(ctx context.Context, tx *sql.Tx, st settings.Setting[string, string], raw json.RawMessage) (string, func(), error) {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", nil, fmt.Errorf("invalid string: %w", err)
	}
	return st.SetTx(ctx, tx, v)
}

func callUint16Setting(ctx context.Context, st settings.Setting[uint16, uint16], raw json.RawMessage) error {
	var v uint16
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	return st.Set(ctx, v)
}

func callUint32Setting(ctx context.Context, st settings.Setting[uint32, uint32], raw json.RawMessage) error {
	var v uint32
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid ASN: %w", err)
	}
	return st.Set(ctx, v)
}

func callBoolValidate(st settings.Setting[bool, bool], raw json.RawMessage) error {
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid bool: %w", err)
	}
	return st.Validate(v)
}

func callIntValidate(st settings.Setting[int, int], raw json.RawMessage) error {
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid int: %w", err)
	}
	return st.Validate(v)
}

func callStringValidate(st settings.Setting[string, string], raw json.RawMessage) error {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid string: %w", err)
	}
	return st.Validate(v)
}

func callUint16Validate(st settings.Setting[uint16, uint16], raw json.RawMessage) error {
	var v uint16
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	return st.Validate(v)
}

func callUint32Validate(st settings.Setting[uint32, uint32], raw json.RawMessage) error {
	var v uint32
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid ASN: %w", err)
	}
	return st.Validate(v)
}

// apiSettingsPurgeMetrics handles POST /api/admin/settings/purge-metrics
func (s *Server) apiSettingsPurgeMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := s.store.DB.ExecContext(ctx, "DELETE FROM user_snapshots"); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if _, err := s.store.DB.ExecContext(ctx, "DELETE FROM feed_snapshots"); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	if _, err := s.store.DB.ExecContext(ctx, "DELETE FROM user_prefix_history"); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}
