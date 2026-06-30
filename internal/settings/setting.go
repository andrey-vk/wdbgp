package settings

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Store is the persistence interface for settings.
type Store interface {
	GetAllSettings(ctx context.Context) (map[string]string, error)
	SaveSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error
}

// SettingJSON represents a setting value for JSON serialization.
type SettingJSON[T any] struct {
	Value        *T   `json:"value"`
	DefaultValue T    `json:"default_value"`
	EnvOverride  bool `json:"env_override"`
}

// Setting holds a typed configuration value with env/db/default precedence.
type Setting[T any] struct {
	mu         sync.RWMutex
	value      T
	defaultVal T
	envVal     *T
	dbVal      *T
	dbKey      string
	envVar     string
	store      Store
	parse      func(string) (T, error)
}

// newSetting creates a new Setting.
//
// Precedence: env var → DB value → default.
// If the env var is set but fails to parse, an error is returned.
// If the DB value exists but fails to parse, it is silently ignored and the default is used.
func newSetting[T any](defaultVal T, dbKey, envVar string, parse func(string) (T, error), store Store) (*Setting[T], error) {
	s := &Setting[T]{
		value:      defaultVal,
		defaultVal: defaultVal,
		dbKey:      dbKey,
		envVar:     envVar,
		store:      store,
		parse:      parse,
	}

	// Env var takes highest precedence.
	if envVar != "" {
		envStr := os.Getenv(envVar)
		if envStr != "" {
			val, err := parse(envStr)
			if err != nil {
				return nil, fmt.Errorf("settings: invalid value for %s=%q: %w", envVar, envStr, err)
			}
			s.envVal = &val
			s.value = val
			return s, nil
		}
	}

	// Fall back to DB value.
	settings, err := store.GetAllSettings(context.Background())
	if err != nil {
		// If we can't load settings, use defaults.
		return s, nil
	}

	if dbStr, ok := settings[dbKey]; ok {
		if val, parseErr := parse(dbStr); parseErr == nil {
			s.dbVal = &val
			s.value = val
		}
		// Invalid DB value is silently ignored — dbVal stays nil.
	}

	return s, nil
}

// Get returns the current effective value.
func (s *Setting[T]) Get() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Set saves a new value to the store. Returns an error if the setting is
// overridden by an environment variable.
func (s *Setting[T]) Set(ctx context.Context, v T) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.envVal != nil {
		return fmt.Errorf("settings: cannot set %s, overridden by %s", s.dbKey, s.envVar)
	}

	s.value = v
	val := v
	s.dbVal = &val

	return s.store.SaveSetting(ctx, s.dbKey, fmt.Sprintf("%v", v))
}

// Reset deletes the stored database value and reverts to the default.
// Returns an error if the setting is overridden by an environment variable.
func (s *Setting[T]) Reset(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.envVal != nil {
		return fmt.Errorf("settings: cannot reset %s, overridden by %s", s.dbKey, s.envVar)
	}

	s.dbVal = nil
	s.value = s.defaultVal

	return s.store.DeleteSetting(ctx, s.dbKey)
}

// IsEnvSet returns true if the setting value comes from an environment variable.
func (s *Setting[T]) IsEnvSet() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.envVal != nil
}

// HasDBValue returns true if the setting has a stored database value (and is
// not overridden by an environment variable).
func (s *Setting[T]) HasDBValue() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbVal != nil && s.envVal == nil
}

// JSON returns a serializable representation of the setting suitable for
// API responses.
func (s *Setting[T]) JSON() SettingJSON[T] {
	s.mu.RLock()
	defer s.mu.RUnlock()

	j := SettingJSON[T]{
		DefaultValue: s.defaultVal,
		EnvOverride:  s.envVal != nil,
	}

	if s.envVal != nil {
		j.Value = s.envVal
	} else if s.dbVal != nil {
		j.Value = s.dbVal
	}
	// If neither, Value remains nil (the zero value for *T).

	return j
}

// parseBool parses a boolean string, accepting strconv.ParseBool's input set
// ("1", "t", "T", "TRUE", "true", "True", "0", "f", "F", "FALSE", "false", "False").
func parseBool(s string) (bool, error) {
	s = strings.TrimSpace(s)
	return strconv.ParseBool(s)
}

// parseInt parses an integer string via strconv.Atoi.
func parseInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	return strconv.Atoi(s)
}

// parseString is the identity parser — it returns the input unchanged.
func parseString(s string) (string, error) {
	return s, nil
}
