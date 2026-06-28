package web

import (
	"os"
	"strconv"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/store"
)

func countEnabledFeeds(feeds []store.Feed) int {
	count := 0
	for _, feed := range feeds {
		if feed.Enabled {
			count++
		}
	}
	return count
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

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
