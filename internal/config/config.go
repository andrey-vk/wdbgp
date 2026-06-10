package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DBPath            string
	Host              string
	Port              int
	BGPListenPort     int32
	LocalASN          uint32
	RouterID          string
	LocalAddressV4    string
	LocalAddressV6    string
	AdminPassword     string
	SessionSecret     string
	AdminCookieSecure string
	DefaultLanguage   string
	TrustProxyHeader  bool
	SyncInterval      time.Duration
	SecurityHeaders   bool
	RateLimitLogin    int
	RateLimitAdmin    int
	SessionMaxAge     int
	LogLevel          string
	LogFormat         string
}

func Load() (Config, error) {
	port, err := integer("WDBGP_PORT", 8080)
	if err != nil {
		return Config{}, err
	}
	asn, err := unsignedInteger32("WDBGP_LOCAL_ASN", 64512)
	if err != nil {
		return Config{}, err
	}
	if asn == 0 {
		return Config{}, fmt.Errorf("WDBGP_LOCAL_ASN must be between 1 and %d", uint64(^uint32(0)))
	}
	syncSeconds, err := integer("WDBGP_SYNC_INTERVAL", 3600)
	if err != nil {
		return Config{}, err
	}
	bgpPort, err := integer32("WDBGP_BGP_PORT", 179)
	if err != nil {
		return Config{}, err
	}
	rateLimitLogin, err := integer("WDBGP_RATE_LIMIT_LOGIN", 5)
	if err != nil {
		return Config{}, err
	}
	rateLimitAdmin, err := integer("WDBGP_RATE_LIMIT_ADMIN", 30)
	if err != nil {
		return Config{}, err
	}
	sessionMaxAge, err := integer("WDBGP_SESSION_MAX_AGE", 28800) // 8 hours in seconds
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		DBPath:            env("WDBGP_DB", "/data/wdbgp.sqlite3"),
		Host:              env("WDBGP_HOST", "0.0.0.0"),
		Port:              port,
		BGPListenPort:     bgpPort,
		LocalASN:          asn,
		RouterID:          env("WDBGP_ROUTER_ID", "192.0.2.1"),
		LocalAddressV4:    env("WDBGP_BGP_LOCAL_ADDRESS", env("WDBGP_BIRD_LOCAL_ADDRESS", "192.0.2.2")),
		LocalAddressV6:    env("WDBGP_BGP_LOCAL_ADDRESS_V6", env("WDBGP_BIRD_LOCAL_ADDRESS_V6", "")),
		AdminPassword:     os.Getenv("WDBGP_ADMIN_PASSWORD"),
		SessionSecret:     os.Getenv("WDBGP_SESSION_SECRET"),
		AdminCookieSecure: strings.ToLower(env("WDBGP_ADMIN_COOKIE_SECURE", "auto")),
		DefaultLanguage:   strings.ToLower(env("WDBGP_DEFAULT_LANGUAGE", "en")),
		TrustProxyHeader:  boolean("WDBGP_TRUST_PROXY_HEADERS"),
		SyncInterval:      time.Duration(syncSeconds) * time.Second,
		SecurityHeaders:   boolean("WDBGP_SECURITY_HEADERS"),
		RateLimitLogin:    rateLimitLogin,
		RateLimitAdmin:    rateLimitAdmin,
		SessionMaxAge:     sessionMaxAge,
		LogLevel:          env("WDBGP_LOG_LEVEL", "INFO"),
		LogFormat:         env("WDBGP_LOG_FORMAT", "text"),
	}
	return cfg, nil
}

func (c Config) ListenAddress() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func (c Config) ValidateServe() error {
	if c.AdminPassword == "" || c.SessionSecret == "" {
		return fmt.Errorf("WDBGP_ADMIN_PASSWORD and WDBGP_SESSION_SECRET are required")
	}
	switch c.AdminCookieSecure {
	case "", "auto", "true", "false":
	default:
		return fmt.Errorf("WDBGP_ADMIN_COOKIE_SECURE must be auto, true, or false")
	}
	switch c.DefaultLanguage {
	case "", "en", "ru":
	default:
		return fmt.Errorf("WDBGP_DEFAULT_LANGUAGE must be en or ru")
	}
	if c.LocalASN == 0 {
		return fmt.Errorf("WDBGP_LOCAL_ASN must be greater than zero")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("WDBGP_PORT must be between 1 and 65535")
	}
	if c.BGPListenPort < 1 || c.BGPListenPort > 65535 {
		return fmt.Errorf("WDBGP_BGP_PORT must be between 1 and 65535")
	}
	routerID, err := netip.ParseAddr(c.RouterID)
	if err != nil || !routerID.Is4() {
		return fmt.Errorf("WDBGP_ROUTER_ID must be an IPv4 address")
	}
	localV4, err := netip.ParseAddr(c.LocalAddressV4)
	if err != nil || !localV4.Is4() {
		return fmt.Errorf("WDBGP_BGP_LOCAL_ADDRESS must be an IPv4 address")
	}
	if c.LocalAddressV6 != "" {
		localV6, err := netip.ParseAddr(c.LocalAddressV6)
		if err != nil || !localV6.Is6() {
			return fmt.Errorf("WDBGP_BGP_LOCAL_ADDRESS_V6 must be an IPv6 address")
		}
	}
	if c.SyncInterval <= 0 {
		return fmt.Errorf("WDBGP_SYNC_INTERVAL must be greater than zero")
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func integer(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return int(number), nil
}

func integer32(name string, fallback int32) (int32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return int32(number), nil
}

func unsignedInteger32(name string, fallback uint32) (uint32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	if strings.HasPrefix(strings.TrimSpace(value), "-") {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, uint64(^uint32(0)))
	}
	number, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return uint32(number), nil
}

func boolean(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
