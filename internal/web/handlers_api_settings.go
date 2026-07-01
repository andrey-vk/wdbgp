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

// apiSettingsPut handles PUT /api/admin/settings.
func (s *Server) apiSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "Invalid request body"})
		return
	}

	ctx := r.Context()
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
	}

	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

// setSetting parses raw JSON and calls the typed Set method on the setting.
func (s *Server) setSetting(ctx context.Context, key string, raw json.RawMessage) error {
	switch key {
	case "active_dial":
		return callBoolSetting(s.settings.ActiveDial, ctx, raw)
	case "adapter_backup_dir":
		return callStringSetting(s.settings.AdapterBackupDir, ctx, raw)
	case "adapter_backup_max":
		return callIntSetting(s.settings.AdapterBackupMax, ctx, raw)
	case "admin_cookie_secure":
		return callStringSetting(s.settings.AdminCookieSecure, ctx, raw)
	case "allow_dynamic_peers":
		return callBoolSetting(s.settings.AllowDynamicPeers, ctx, raw)
	case "auto_restore_enabled":
		return callBoolSetting(s.settings.AutoRestoreEnabled, ctx, raw)
	case "bgp_port":
		return callIntSetting(s.settings.BGPPort, ctx, raw)
	case "backup_dir":
		return callStringSetting(s.settings.BackupDir, ctx, raw)
	case "backup_enabled":
		return callBoolSetting(s.settings.BackupEnabled, ctx, raw)
	case "default_language":
		return callStringSetting(s.settings.DefaultLanguage, ctx, raw)
	case "default_web_auth":
		return callStringSetting(s.settings.DefaultWebAuth, ctx, raw)
	case "filter_allow":
		return callStringSetting(s.settings.FilterAllow, ctx, raw)
	case "filter_deny":
		return callStringSetting(s.settings.FilterDeny, ctx, raw)
	case "host":
		return callStringSetting(s.settings.Host, ctx, raw)
	case "js_max_call_stack":
		return callIntSetting(s.settings.JSMaxCallStack, ctx, raw)
	case "js_max_entries":
		return callIntSetting(s.settings.JSMaxEntries, ctx, raw)
	case "js_max_requests":
		return callIntSetting(s.settings.JSMaxRequests, ctx, raw)
	case "js_max_response":
		return callIntSetting(s.settings.JSMaxResponseBytes, ctx, raw)
	case "js_max_source":
		return callIntSetting(s.settings.JSMaxSourceBytes, ctx, raw)
	case "js_max_total":
		return callIntSetting(s.settings.JSMaxTotalBytes, ctx, raw)
	case "js_timeout":
		return callIntSetting(s.settings.JSTimeout, ctx, raw)
	case "local_asn":
		return callIntSetting(s.settings.LocalASN, ctx, raw)
	case "local_address_v4":
		return callStringSetting(s.settings.LocalAddressV4, ctx, raw)
	case "local_address_v6":
		return callStringSetting(s.settings.LocalAddressV6, ctx, raw)
	case "log_format":
		return callStringSetting(s.settings.LogFormat, ctx, raw)
	case "log_level":
		return callStringSetting(s.settings.LogLevel, ctx, raw)
	case "metrics_enabled":
		return callBoolSetting(s.settings.MetricsEnabled, ctx, raw)
	case "metrics_history_days":
		return callIntSetting(s.settings.MetricsHistoryDays, ctx, raw)
	case "port":
		return callIntSetting(s.settings.Port, ctx, raw)
	case "rate_limit_admin":
		return callIntSetting(s.settings.RateLimitAdmin, ctx, raw)
	case "rate_limit_login":
		return callIntSetting(s.settings.RateLimitLogin, ctx, raw)
	case "router_id":
		return callStringSetting(s.settings.RouterID, ctx, raw)
	case "security_headers":
		return callBoolSetting(s.settings.SecurityHeaders, ctx, raw)
	case "session_max_age":
		return callIntSetting(s.settings.SessionMaxAge, ctx, raw)
	case "status_allowed":
		return callStringSetting(s.settings.StatusAllowed, ctx, raw)
	case "status_token":
		return callStringSetting(s.settings.StatusToken, ctx, raw)
	case "sync_interval":
		return callIntSetting(s.settings.SyncInterval, ctx, raw)
	case "trust_proxy_headers":
		return callBoolSetting(s.settings.TrustProxyHeaders, ctx, raw)
	case "require_password_for_non_unique_ip":
		return callBoolSetting(s.settings.RequirePasswordForNonUniqueIP, ctx, raw)
	case "db_path":
		return callStringSetting(s.settings.DBPath, ctx, raw)
	case "admin_password":
		return callStringSetting(s.settings.AdminPassword, ctx, raw)
	case "session_secret":
		return callStringSetting(s.settings.SessionSecret, ctx, raw)
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
		return s.settings.Host.Reset(ctx)
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
		return s.settings.Port.Reset(ctx)
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
		return s.settings.DBPath.Reset(ctx)
	case "admin_password":
		return s.settings.AdminPassword.Reset(ctx)
	case "session_secret":
		return s.settings.SessionSecret.Reset(ctx)
	}
	return fmt.Errorf("unknown setting: %s", key)
}

func callBoolSetting(st settings.Setting[bool, bool], ctx context.Context, raw json.RawMessage) error {
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid bool: %w", err)
	}
	return st.Set(ctx, v)
}

func callIntSetting(st settings.Setting[int, int], ctx context.Context, raw json.RawMessage) error {
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid int: %w", err)
	}
	return st.Set(ctx, v)
}

func callStringSetting(st settings.Setting[string, string], ctx context.Context, raw json.RawMessage) error {
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid string: %w", err)
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
