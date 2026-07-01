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

// OnChangeFunc is called after a setting value changes via Set or Reset.
type OnChangeFunc[T any] func(newValue T)

// Setting is the interface for typed configuration values with env/db/default precedence.
type Setting[JSON, Runtime any] interface {
	Get() Runtime
	Set(ctx context.Context, v JSON) error
	Reset(ctx context.Context) error
	IsEnvSet() bool
	HasDBValue(ctx context.Context) bool
	JSON(dbSettings map[string]string) SettingJSON[JSON]
	OnChange(fn OnChangeFunc[Runtime]) func()
}

// simpleSetting[T] implements Setting[T, T] — for basic bool/int/string settings.
type simpleSetting[T any] struct {
	mu          sync.RWMutex
	value       T
	defaultVal  T
	dbKey       string
	envVar      string
	store       Store
	parse       func(string) (T, error)
	validate    func(T) error
	callbacks   []OnChangeFunc[T]
	callbacksMu sync.Mutex
}

// complexSetting[T] implements Setting[string, T] — for settings where JSON is always string.
type complexSetting[T any] struct {
	mu          sync.RWMutex
	value       T
	defaultVal  string
	dbKey       string
	envVar      string
	store       Store
	parse       func(string) (T, error)
	validate    func(T) error
	callbacks   []OnChangeFunc[T]
	callbacksMu sync.Mutex
}

// newSimple creates a new simpleSetting[T].
//
// Precedence: env var -> DB value -> default. The effective value is
// validated regardless of where it came from — an out-of-range env var must
// fail startup just as surely as an out-of-range value set through the API.
// If the env var is set but fails to parse, an error is returned.
// If the DB value exists but fails to parse, the default is used silently.
func newSimple[T any](defaultVal T, dbKey, envVar string, parse func(string) (T, error), validate func(T) error, store Store, dbSettings map[string]string) (*simpleSetting[T], error) {
	s := &simpleSetting[T]{
		value:      defaultVal,
		defaultVal: defaultVal,
		dbKey:      dbKey,
		envVar:     envVar,
		store:      store,
		parse:      parse,
		validate:   validate,
	}

	// Env var takes highest precedence.
	envSource := ""
	if envVar != "" {
		if envStr := os.Getenv(envVar); envStr != "" {
			val, err := parse(envStr)
			if err != nil {
				return nil, fmt.Errorf("settings: invalid value for %s=%q: %w", envVar, envStr, err)
			}
			s.value = val
			envSource = fmt.Sprintf("%s=%q", envVar, envStr)
		}
	}

	// Fall back to DB value (pre-loaded by caller to avoid N per-field queries),
	// only if the env var didn't already win.
	if envSource == "" {
		if dbStr, ok := dbSettings[dbKey]; ok {
			if val, parseErr := parse(dbStr); parseErr == nil {
				s.value = val
			}
			// Invalid DB value is silently ignored — value stays at default.
		}
	}

	// Validate the effective value, whichever source it came from.
	if s.validate != nil {
		if err := s.validate(s.value); err != nil {
			source := envSource
			if source == "" {
				source = "default"
			}
			return nil, fmt.Errorf("settings: invalid value for %s (%s): %w", dbKey, source, err)
		}
	}

	return s, nil
}

// newComplex creates a new complexSetting[T].
//
// defaultJSON is the default in DB/JSON format (a string).
// Precedence: env var -> DB value -> defaultJSON (parsed). The effective
// value is validated regardless of where it came from — an invalid env var
// must fail startup just as surely as an invalid value set through the API.
// If env is set but fails to parse, an error is returned.
// If DB value exists but fails to parse, parse(defaultJSON) is used silently.
func newComplex[T any](defaultJSON string, dbKey, envVar string, parse func(string) (T, error), validate func(T) error, store Store, dbSettings map[string]string) (*complexSetting[T], error) {
	s := &complexSetting[T]{
		defaultVal: defaultJSON,
		dbKey:      dbKey,
		envVar:     envVar,
		store:      store,
		parse:      parse,
		validate:   validate,
	}

	// Env var takes highest precedence.
	envSource := ""
	if envVar != "" {
		if envStr := os.Getenv(envVar); envStr != "" {
			val, err := parse(envStr)
			if err != nil {
				return nil, fmt.Errorf("settings: invalid value for %s=%q: %w", envVar, envStr, err)
			}
			s.value = val
			envSource = fmt.Sprintf("%s=%q", envVar, envStr)
		}
	}

	if envSource == "" {
		// Fall back to DB value (pre-loaded by caller).
		useDefault := true
		if dbStr, ok := dbSettings[dbKey]; ok {
			if val, parseErr := parse(dbStr); parseErr == nil {
				s.value = val
				useDefault = false
			}
			// Invalid DB value -> fall back to parsed default.
		}
		if useDefault {
			val, err := parse(defaultJSON)
			if err != nil {
				return nil, fmt.Errorf("settings: default value %q for %s does not parse: %w", defaultJSON, dbKey, err)
			}
			s.value = val
		}
	}

	// Validate the effective value, whichever source it came from.
	if validate != nil {
		if err := validate(s.value); err != nil {
			source := envSource
			if source == "" {
				source = "default"
			}
			return nil, fmt.Errorf("settings: invalid value for %s (%s): %w", dbKey, source, err)
		}
	}

	return s, nil
}

// Get returns the current effective value.
func (s *simpleSetting[T]) Get() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Get returns the current effective value.
func (s *complexSetting[T]) Get() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Set saves a new value to the store.
func (s *simpleSetting[T]) Set(ctx context.Context, v T) error {
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot set %s, overridden by %s", s.dbKey, s.envVar)
	}

	raw := fmt.Sprintf("%v", v)
	parsed, err := s.parse(raw)
	if err != nil {
		return fmt.Errorf("settings: invalid value for %s=%q: %w", s.dbKey, raw, err)
	}

	// Validate domain rules.
	if s.validate != nil {
		if err := s.validate(parsed); err != nil {
			return fmt.Errorf("settings: invalid value for %s: %w", s.dbKey, err)
		}
	}

	s.mu.Lock()
	s.value = parsed
	s.mu.Unlock()

	if err := s.store.SaveSetting(ctx, s.dbKey, raw); err != nil {
		return err
	}

	s.fireCallbacks(parsed)
	return nil
}

// Set saves a new value to the store.
func (s *complexSetting[T]) Set(ctx context.Context, v string) error {
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot set %s, overridden by %s", s.dbKey, s.envVar)
	}

	parsed, err := s.parse(v)
	if err != nil {
		return fmt.Errorf("settings: invalid value for %s=%q: %w", s.dbKey, v, err)
	}

	// Validate domain rules.
	if s.validate != nil {
		if err := s.validate(parsed); err != nil {
			return fmt.Errorf("settings: invalid value for %s: %w", s.dbKey, err)
		}
	}

	s.mu.Lock()
	s.value = parsed
	s.mu.Unlock()

	if err := s.store.SaveSetting(ctx, s.dbKey, v); err != nil {
		return err
	}

	s.fireCallbacks(parsed)
	return nil
}

// Reset deletes the stored database value and reverts to the default.
func (s *simpleSetting[T]) Reset(ctx context.Context) error {
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot reset %s, overridden by %s", s.dbKey, s.envVar)
	}

	s.mu.Lock()
	s.value = s.defaultVal
	s.mu.Unlock()

	if err := s.store.DeleteSetting(ctx, s.dbKey); err != nil {
		return err
	}

	s.fireCallbacks(s.defaultVal)
	return nil
}

// Reset deletes the stored database value and reverts to parse(defaultJSON).
func (s *complexSetting[T]) Reset(ctx context.Context) error {
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot reset %s, overridden by %s", s.dbKey, s.envVar)
	}

	defaultParsed, err := s.parse(s.defaultVal)
	if err != nil {
		return fmt.Errorf("settings: default value %q for %s does not parse: %w", s.defaultVal, s.dbKey, err)
	}

	s.mu.Lock()
	s.value = defaultParsed
	s.mu.Unlock()

	if err := s.store.DeleteSetting(ctx, s.dbKey); err != nil {
		return err
	}

	s.fireCallbacks(defaultParsed)
	return nil
}

// IsEnvSet returns true if the setting value comes from an environment variable.
func (s *simpleSetting[T]) IsEnvSet() bool {
	if s.envVar == "" {
		return false
	}
	return os.Getenv(s.envVar) != ""
}

// IsEnvSet returns true if the setting value comes from an environment variable.
func (s *complexSetting[T]) IsEnvSet() bool {
	if s.envVar == "" {
		return false
	}
	return os.Getenv(s.envVar) != ""
}

// HasDBValue returns true if the setting has a stored database value.
func (s *simpleSetting[T]) HasDBValue(ctx context.Context) bool {
	settings, err := s.store.GetAllSettings(ctx)
	if err != nil {
		return false
	}
	_, ok := settings[s.dbKey]
	return ok
}

// HasDBValue returns true if the setting has a stored database value.
func (s *complexSetting[T]) HasDBValue(ctx context.Context) bool {
	settings, err := s.store.GetAllSettings(ctx)
	if err != nil {
		return false
	}
	_, ok := settings[s.dbKey]
	return ok
}

// JSON returns a serializable representation of the setting.
func (s *simpleSetting[T]) JSON(dbSettings map[string]string) SettingJSON[T] {
	j := SettingJSON[T]{DefaultValue: s.defaultVal}

	if envStr := os.Getenv(s.envVar); s.envVar != "" && envStr != "" {
		// An env override always counts as EnvOverride, even if it doesn't
		// currently parse — Value just stays nil in that case rather than
		// showing a misleading zero value.
		if v, err := s.parse(envStr); err == nil {
			j.Value = &v
		}
		j.EnvOverride = true
		return j
	}
	if dbStr, ok := dbSettings[s.dbKey]; ok {
		if v, err := s.parse(dbStr); err == nil {
			j.Value = &v
		}
	}
	// else Value remains nil
	return j
}

// JSON returns a serializable representation of the setting.
func (s *complexSetting[T]) JSON(dbSettings map[string]string) SettingJSON[string] {
	j := SettingJSON[string]{DefaultValue: s.defaultVal}

	if envStr := os.Getenv(s.envVar); s.envVar != "" && envStr != "" {
		j.Value = &envStr
		j.EnvOverride = true
		return j
	}
	if dbStr, ok := dbSettings[s.dbKey]; ok {
		j.Value = &dbStr
	}
	// else Value remains nil
	return j
}

// OnChange registers a callback that fires after a successful Set or Reset.
func (s *simpleSetting[T]) OnChange(fn OnChangeFunc[T]) func() {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()

	s.callbacks = append(s.callbacks, fn)
	idx := len(s.callbacks) - 1

	return func() {
		s.callbacksMu.Lock()
		defer s.callbacksMu.Unlock()
		s.callbacks[idx] = nil
	}
}

// OnChange registers a callback that fires after a successful Set or Reset.
func (s *complexSetting[T]) OnChange(fn OnChangeFunc[T]) func() {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()

	s.callbacks = append(s.callbacks, fn)
	idx := len(s.callbacks) - 1

	return func() {
		s.callbacksMu.Lock()
		defer s.callbacksMu.Unlock()
		s.callbacks[idx] = nil
	}
}

// fireCallbacks invokes all registered callbacks with the given value.
func (s *simpleSetting[T]) fireCallbacks(v T) {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()
	for _, fn := range s.callbacks {
		if fn != nil {
			fn(v)
		}
	}
}

// fireCallbacks invokes all registered callbacks with the given value.
func (s *complexSetting[T]) fireCallbacks(v T) {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()
	for _, fn := range s.callbacks {
		if fn != nil {
			fn(v)
		}
	}
}

// parseBool parses a boolean string, accepting strconv.ParseBool's input set.
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
