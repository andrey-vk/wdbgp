package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/andrey-vk/wdbgp/internal/settings"
)

// apiSettingsGet handles GET /api/admin/settings.
func (s *Server) apiSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings.JSON(r.Context()))
}

// filterKeysAffectBGP are the setting keys that change what routes get
// announced. Unlike the restart-only BGP identity settings (ASN, router ID,
// etc.), these take effect immediately — reconcileLocked() just needs to
// run again with the new value already persisted, no speaker restart.
var filterKeysAffectBGP = map[string]bool{"filter_allow": true, "filter_deny": true}

// apiSettingsPut handles PUT /api/admin/settings.
func (s *Server) apiSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	ctx := r.Context()
	reconcileNeeded := false
	for key, raw := range body {
		if string(raw) == "null" {
			// Reset to default
			if err := s.resetSetting(ctx, key); err != nil {
				writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
				return
			}
		} else {
			// Set new value
			if err := s.setSetting(ctx, key, raw); err != nil {
				writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: err.Error()})
				return
			}
		}
		if filterKeysAffectBGP[key] {
			reconcileNeeded = true
		}
	}

	// Global route filters change what DesiredPrefixes() computes, so a
	// change here must apply now — otherwise already-announced prefixes
	// keep using the old filter set until the next scheduled sync/reconcile
	// (up to sync_interval seconds later), which for a deny filter means
	// routes the admin just tried to block stay live in the meantime.
	if reconcileNeeded && s.bgp != nil {
		if err := s.bgp.Reconcile(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "Settings saved but BGP reconciliation failed: " + err.Error()})
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
	case "allow_dynamic_peers":
		return callBoolSetting(ctx, s.settings.AllowDynamicPeers, raw)
	case "auto_restore_enabled":
		return callBoolSetting(ctx, s.settings.AutoRestoreEnabled, raw)
	case "bgp_port":
		return callUint16Setting(ctx, s.settings.BGPPort, raw)
	case "backup_dir":
		return callStringSetting(ctx, s.settings.BackupDir, raw)
	case "backup_enabled":
		return callBoolSetting(ctx, s.settings.BackupEnabled, raw)
	case "default_language":
		return callStringSetting(ctx, s.settings.DefaultLanguage, raw)
	case "default_web_auth":
		return callStringSetting(ctx, s.settings.DefaultWebAuth, raw)
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
	case "allow_dynamic_peers":
		return s.settings.AllowDynamicPeers.Reset(ctx)
	case "auto_restore_enabled":
		return s.settings.AutoRestoreEnabled.Reset(ctx)
	case "bgp_port":
		return s.settings.BGPPort.Reset(ctx)
	case "backup_dir":
		return s.settings.BackupDir.Reset(ctx)
	case "backup_enabled":
		return s.settings.BackupEnabled.Reset(ctx)
	case "default_language":
		return s.settings.DefaultLanguage.Reset(ctx)
	case "default_web_auth":
		return s.settings.DefaultWebAuth.Reset(ctx)
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
		return s.settings.AdminPassword.Reset(ctx)
	case "session_secret":
		return s.settings.SessionSecret.Reset(ctx)
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
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}
