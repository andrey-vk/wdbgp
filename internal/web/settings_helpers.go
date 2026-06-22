package web

import (
	"net/http"
	"os"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

// --- Settings page field definitions (shared infrastructure) ---

type settingField struct {
	Key          string            // DB key like "default_language"
	Name         string            // i18n key for short name like "settings.default_language"
	EnvVar       string            // ENV var name like "WDBGP_DEFAULT_LANGUAGE"
	Type         string            // "text", "number", "select", "bool", "password"
	Options      map[string]string // for select: value→label i18n key
	Section      string            // i18n key for section title like "settings.section_general"
	Restart      bool              // requires app restart
	Placeholder  string            // placeholder text (i18n key)
	Value        string            // current value (populated at render time)
	EnvOverride  bool              // whether an ENV var overrides this setting
	DefaultValue string            // default value text (for placeholder when Value is empty)
}

type settingSection struct {
	TitleKey string // i18n key
	Fields   []settingField
}

func allSettings() []settingField {
	return []settingField{
		// General
		{Key: "default_language", Name: "settings.default_language", EnvVar: "WDBGP_DEFAULT_LANGUAGE", Type: "select", Options: map[string]string{"en": "language.english", "ru": "language.russian"}, Section: "settings.section_general"},
		{Key: "session_max_age", Name: "settings.session_max_age", EnvVar: "WDBGP_SESSION_MAX_AGE", Type: "number", Section: "settings.section_general", Placeholder: "settings.session_max_age_placeholder"},
		{Key: "admin_cookie_secure", Name: "settings.admin_cookie_secure", EnvVar: "WDBGP_ADMIN_COOKIE_SECURE", Type: "select", Options: map[string]string{"auto": "settings.auto", "true": "settings.true", "false": "settings.false"}, Section: "settings.section_general"},
		{Key: "trust_proxy_headers", Name: "settings.trust_proxy_headers", EnvVar: "WDBGP_TRUST_PROXY_HEADERS", Type: "bool", Section: "settings.section_general"},
		{Key: "security_headers", Name: "settings.security_headers", EnvVar: "WDBGP_SECURITY_HEADERS", Type: "bool", Section: "settings.section_general"},
		{Key: "default_web_auth", Name: "settings.default_web_auth", EnvVar: "WDBGP_DEFAULT_WEB_AUTH", Type: "select", Options: map[string]string{"network": "users.web_auth_network", "login": "users.web_auth_login", "both": "users.web_auth_both", "any": "users.web_auth_any"}, Section: "settings.section_general"},
		{Key: "status_allowed", Name: "settings.status_allowed", EnvVar: "WDBGP_STATUS_ALLOWED", Type: "text", Section: "settings.section_general", Placeholder: "settings.status_allowed_placeholder"},
		{Key: "status_token", Name: "settings.status_token", EnvVar: "WDBGP_STATUS_TOKEN", Type: "text", Section: "settings.section_general", Placeholder: "settings.status_token_placeholder"},
		{Key: "adapter_backup_dir", Name: "settings.adapter_backup_dir", EnvVar: "WDBGP_ADAPTER_BACKUP_DIR", Type: "text", Section: "settings.section_general", Placeholder: "settings.adapter_backup_dir_placeholder"},
		{Key: "adapter_backup_max", Name: "settings.adapter_backup_max", EnvVar: "WDBGP_ADAPTER_BACKUP_MAX", Type: "number", Section: "settings.section_general", Placeholder: "settings.adapter_backup_max_placeholder"},

		// Rate Limiting
		{Key: "rate_limit_login", Name: "settings.rate_limit_login", EnvVar: "WDBGP_RATE_LIMIT_LOGIN", Type: "number", Section: "settings.section_rate_limit", Placeholder: "settings.rate_limit_login_placeholder"},
		{Key: "rate_limit_admin", Name: "settings.rate_limit_admin", EnvVar: "WDBGP_RATE_LIMIT_ADMIN", Type: "number", Section: "settings.section_rate_limit", Placeholder: "settings.rate_limit_admin_placeholder"},

		// Logging
		{Key: "log_level", Name: "settings.log_level", EnvVar: "WDBGP_LOG_LEVEL", Type: "select", Options: map[string]string{"DEBUG": "DEBUG", "INFO": "INFO", "WARN": "WARN", "ERROR": "ERROR", "FATAL": "FATAL", "PANIC": "PANIC"}, Section: "settings.section_logging"},
		{Key: "log_format", Name: "settings.log_format", EnvVar: "WDBGP_LOG_FORMAT", Type: "select", Options: map[string]string{"text": "text", "json": "json"}, Section: "settings.section_logging"},

		// Feed Sync
		{Key: "sync_interval", Name: "settings.sync_interval", EnvVar: "WDBGP_SYNC_INTERVAL", Type: "number", Section: "settings.section_sync", Placeholder: "settings.sync_interval_placeholder"},

		// JavaScript Runtime
		{Key: "js_timeout", Name: "settings.js_timeout", EnvVar: "WDBGP_JS_TIMEOUT", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_timeout_placeholder"},
		{Key: "js_max_source", Name: "settings.js_max_source", EnvVar: "WDBGP_JS_MAX_SOURCE", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_source_placeholder"},
		{Key: "js_max_response", Name: "settings.js_max_response", EnvVar: "WDBGP_JS_MAX_RESPONSE", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_response_placeholder"},
		{Key: "js_max_total", Name: "settings.js_max_total", EnvVar: "WDBGP_JS_MAX_TOTAL", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_total_placeholder"},
		{Key: "js_max_entries", Name: "settings.js_max_entries", EnvVar: "WDBGP_JS_MAX_ENTRIES", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_entries_placeholder"},
		{Key: "js_max_requests", Name: "settings.js_max_requests", EnvVar: "WDBGP_JS_MAX_REQUESTS", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_requests_placeholder"},
		{Key: "js_max_call_stack", Name: "settings.js_max_call_stack", EnvVar: "WDBGP_JS_MAX_CALL_STACK", Type: "number", Section: "settings.section_js", Placeholder: "settings.js_max_call_stack_placeholder"},

		// BGP (requires restart)
		{Key: "bgp_port", Name: "settings.bgp_port", EnvVar: "WDBGP_BGP_PORT", Type: "number", Section: "settings.section_bgp", Restart: true},
		{Key: "local_asn", Name: "settings.local_asn", EnvVar: "WDBGP_LOCAL_ASN", Type: "number", Section: "settings.section_bgp", Restart: true},
		{Key: "router_id", Name: "settings.router_id", EnvVar: "WDBGP_ROUTER_ID", Type: "text", Section: "settings.section_bgp", Restart: true},
		{Key: "local_address_v4", Name: "settings.local_address_v4", EnvVar: "WDBGP_BGP_LOCAL_ADDRESS", Type: "text", Section: "settings.section_bgp", Restart: true},
		{Key: "local_address_v6", Name: "settings.local_address_v6", EnvVar: "WDBGP_BGP_LOCAL_ADDRESS_V6", Type: "text", Section: "settings.section_bgp", Restart: true},

		// Network (requires restart)
		{Key: "host", Name: "settings.host", EnvVar: "WDBGP_HOST", Type: "text", Section: "settings.section_network", Restart: true},
		{Key: "port", Name: "settings.port", EnvVar: "WDBGP_PORT", Type: "number", Section: "settings.section_network", Restart: true},
	}
}

func allSettingKeys() []string {
	settings := allSettings()
	keys := make([]string, len(settings))
	for i, s := range settings {
		keys[i] = s.Key
	}
	return keys
}

func buildSettingsSections(cfg config.Config, dbSettings map[string]string) []settingSection {
	all := allSettings()
	sectionMap := make(map[string][]settingField)
	sectionOrder := []string{} // preserve order

	for _, f := range all {
		// Populate value and env override
		if v := os.Getenv(f.EnvVar); v != "" {
			f.Value = v
			f.EnvOverride = true
		} else if v, ok := dbSettings[f.Key]; ok {
			f.Value = v
			f.EnvOverride = false
		} else {
			f.Value = "" // zero value, template shows placeholder/default
			f.EnvOverride = false
			f.DefaultValue = configDefaultValue(cfg, f.Key)
		}

		if _, ok := sectionMap[f.Section]; !ok {
			sectionOrder = append(sectionOrder, f.Section)
		}
		sectionMap[f.Section] = append(sectionMap[f.Section], f)
	}

	sections := make([]settingSection, 0, len(sectionOrder))
	for _, sKey := range sectionOrder {
		sections = append(sections, settingSection{
			TitleKey: sKey,
			Fields:   sectionMap[sKey],
		})
	}
	return sections
}

func routeFiltersFromForm(r *http.Request) (store.RouteFilters, error) {
	filters := store.RouteFilters{
		Allow: splitCIDRs(r.FormValue("filter_allow")),
		Deny:  splitCIDRs(r.FormValue("filter_deny")),
	}
	return store.NormalizeRouteFilters(filters)
}

func splitCIDRs(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}
