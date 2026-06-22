package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func isHtmxRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") != ""
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func formModeID(r *http.Request, fallback int64) (int64, error) {
	raw := strings.TrimSpace(r.FormValue("catalog_mode_id"))
	if raw == "" && fallback > 0 {
		return fallback, nil
	}
	modeID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || modeID <= 0 {
		return 0, fmt.Errorf("invalid catalog mode")
	}
	return modeID, nil
}

func formInt(r *http.Request, key string) int {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return 0
	}
	val, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return val
}

func serviceValue(category, service string) string {
	return url.QueryEscape(category) + ":" + url.QueryEscape(service)
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sortStrings(result)
	return result
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func countEnabledFeeds(feeds []store.Feed) int {
	count := 0
	for _, feed := range feeds {
		if feed.Enabled {
			count++
		}
	}
	return count
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func isEnvOverridden(envVar string) bool {
	return os.Getenv(envVar) != ""
}

func configDefaultValue(cfg config.Config, key string) string {
	switch key {
	case "default_language":
		return cfg.DefaultLanguage
	case "session_max_age":
		return strconv.Itoa(cfg.SessionMaxAge)
	case "admin_cookie_secure":
		return cfg.AdminCookieSecure
	case "trust_proxy_headers":
		return boolStr(cfg.TrustProxyHeader)
	case "security_headers":
		return boolStr(cfg.SecurityHeaders)
	case "default_web_auth":
		return cfg.DefaultWebAuth
	case "rate_limit_login":
		return strconv.Itoa(cfg.RateLimitLogin)
	case "rate_limit_admin":
		return strconv.Itoa(cfg.RateLimitAdmin)
	case "log_level":
		return cfg.LogLevel
	case "log_format":
		return cfg.LogFormat
	case "sync_interval":
		return strconv.Itoa(int(cfg.SyncInterval.Seconds()))
	case "js_timeout":
		return strconv.Itoa(int(cfg.JSTimeout.Seconds()))
	case "js_max_source":
		return strconv.Itoa(cfg.JSMaxSourceBytes)
	case "js_max_response":
		return strconv.Itoa(cfg.JSMaxResponseBytes)
	case "js_max_total":
		return strconv.Itoa(cfg.JSMaxTotalBytes)
	case "js_max_entries":
		return strconv.Itoa(cfg.JSMaxEntries)
	case "js_max_requests":
		return strconv.Itoa(cfg.JSMaxRequests)
	case "js_max_call_stack":
		return strconv.Itoa(cfg.JSMaxCallStack)
	case "bgp_port":
		return strconv.Itoa(int(cfg.BGPListenPort))
	case "local_asn":
		return strconv.FormatUint(uint64(cfg.LocalASN), 10)
	case "router_id":
		return cfg.RouterID
	case "local_address_v4":
		return cfg.LocalAddressV4
	case "local_address_v6":
		return cfg.LocalAddressV6
	case "host":
		return cfg.Host
	case "port":
		return strconv.Itoa(cfg.Port)
	case "adapter_backup_dir":
		return cfg.AdapterBackupDir
	case "adapter_backup_max":
		return strconv.Itoa(cfg.AdapterBackupMax)
	}
	return ""
}

func fieldByKey(key string) *settingField {
	for _, f := range allSettings() {
		if f.Key == key {
			return &f
		}
	}
	return nil
}
