package config

import (
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"path"
	"strconv"
	"strings"
)

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

func boolean(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func validatePort(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 1 || number > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return int(number), nil
}

func validatePort32(name string, fallback int32) (int32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 1 || number > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return int32(number), nil
}

func validateASN(name string, fallback uint32) (uint32, error) {
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
	if number == 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return uint32(number), nil
}

func validateSyncInterval(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero seconds", name)
	}
	// Warn about extremely short intervals but don't reject them
	if number < 60 {
		log.Printf("WARNING: %s=%d is extremely short (less than 60 seconds)", name, number)
	}
	return int(number), nil
}

func validateRateLimit(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 1 {
		return 0, fmt.Errorf("%s must be at least 1 request per minute", name)
	}
	if number > 1000 {
		return 0, fmt.Errorf("%s must not exceed 1000 requests per minute", name)
	}
	return int(number), nil
}

func validateSessionMaxAge(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.ParseInt(value, 10, 0)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if number < 60 {
		return 0, fmt.Errorf("%s must be at least 60 seconds (1 minute)", name)
	}
	if number > 31536000 {
		return 0, fmt.Errorf("%s must not exceed 31536000 seconds (1 year)", name)
	}
	return int(number), nil
}

func validateLogLevel(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	validLevels := map[string]bool{
		"DEBUG":   true,
		"INFO":    true,
		"WARN":    true,
		"WARNING": true,
		"ERROR":   true,
		"FATAL":   true,
		"PANIC":   true,
	}
	upperValue := strings.ToUpper(value)
	if !validLevels[upperValue] {
		return "", fmt.Errorf("%s must be one of: DEBUG, INFO, WARN, ERROR, FATAL, PANIC", name)
	}
	return upperValue, nil
}

func validateLogFormat(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	lowerValue := strings.ToLower(value)
	if lowerValue != "text" && lowerValue != "json" {
		return "", fmt.Errorf("%s must be either 'text' or 'json'", name)
	}
	return lowerValue, nil
}

func validateHost(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	// Check if it's a valid IP address
	if ip := net.ParseIP(value); ip != nil {
		return value, nil
	}
	// Check if it's "localhost"
	if strings.ToLower(value) == "localhost" {
		return value, nil
	}
	// Check for common invalid patterns
	if strings.Contains(value, ":") {
		// Might contain port
		if _, _, err := net.SplitHostPort(value); err == nil {
			return "", fmt.Errorf("%s should not include port number (port is configured separately)", name)
		}
	}
	// Basic hostname validation (without DNS lookup to avoid blocking)
	// Check length and characters
	if len(value) > 255 {
		return "", fmt.Errorf("%s hostname too long (max 255 characters)", name)
	}
	// Check if it looks like a valid hostname
	// Allow underscore which is common in internal networks
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("%s invalid hostname label %q (length must be 1-63)", name, label)
		}
		// Allow letters, digits, hyphen, and underscore
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				return "", fmt.Errorf("%s invalid character %q in hostname label %q", name, r, label)
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%s hostname label cannot start or end with hyphen: %q", name, label)
		}
	}
	return value, nil
}

func validateDBPath(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	// Check for absolute path
	if !strings.HasPrefix(value, "/") {
		// Relative paths are allowed but warn
		// In production, absolute paths are recommended
	}

	// Get parent directory
	dir := path.Dir(value)
	if dir == "." {
		dir = "."
	} else if dir == "/" {
		// Root directory
	} else if dir == "" {
		dir = "."
	}

	// Check if parent directory exists and is writable
	if stat, err := os.Stat(dir); err == nil {
		// Directory exists
		if !stat.IsDir() {
			return "", fmt.Errorf("%s: parent path %s is not a directory", name, dir)
		}
		// Check write permission (simplified check)
		if stat.Mode().Perm()&0200 == 0 {
			return "", fmt.Errorf("%s: directory %s is not writable", name, dir)
		}
	} else if os.IsNotExist(err) {
		// Parent directory doesn't exist
		// Try to check grandparent directory
		grandDir := path.Dir(dir)
		if grandDir != dir { // Not at root
			if stat, err := os.Stat(grandDir); err == nil && stat.IsDir() {
				if stat.Mode().Perm()&0200 == 0 {
					return "", fmt.Errorf("%s: cannot create directory %s - parent %s is not writable", name, dir, grandDir)
				}
		} else if os.IsNotExist(err) {
			log.Printf("WARNING: %s: parent directory %s does not exist", name, dir)
		}
		}
	} else {
		// Other error (permission denied, etc.)
		return "", fmt.Errorf("%s: cannot access directory %s: %w", name, dir, err)
	}

	return value, nil
}

func validateIPv4Address(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		// Check fallback value
		if fallback == "" {
			return "", nil
		}
		value = fallback
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.Is4() {
		return "", fmt.Errorf("%s must be a valid IPv4 address", name)
	}
	return value, nil
}

func validateIPv6Address(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		// Check fallback value
		if fallback == "" {
			return "", nil
		}
		value = fallback
	}
	if value == "" {
		return "", nil
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.Is6() {
		return "", fmt.Errorf("%s must be a valid IPv6 address", name)
	}
	return value, nil
}

func validateAdminCookieSecure(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	lowerValue := strings.ToLower(value)
	if lowerValue != "auto" && lowerValue != "true" && lowerValue != "false" {
		return "", fmt.Errorf("%s must be one of: auto, true, false", name)
	}
	return lowerValue, nil
}

func validateDefaultLanguage(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	lowerValue := strings.ToLower(value)
	if lowerValue != "en" && lowerValue != "ru" {
		return "", fmt.Errorf("%s must be one of: en, ru", name)
	}
	return lowerValue, nil
}

func splitCIDRsEnv(name string) []string {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func validateWebAuthMode(name string, fallback string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	switch value {
	case "network", "login", "both", "any":
		return value, nil
	default:
		return "", fmt.Errorf("%s must be network, login, both, or any", name)
	}
}

func validateBackupMax(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
