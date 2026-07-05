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

	// Validate reports whether v would be accepted by Set — same parse and
	// domain-validation steps, without persisting or mutating anything. Lets
	// a caller validate an entire batch of settings up front (see
	// apiSettingsPut) before applying any of it, so one invalid field in a
	// multi-key request can't leave the others partially applied.
	Validate(v JSON) error

	// dbEnvKeys exposes the storage/env keys for internal introspection only
	// (see TestNoSettingIsBothDBAndEnvInaccessible) — a setting with both
	// empty would be permanently unsettable via any path yet indistinguishable,
	// from the JSON() output, from a normal env-only setting whose env var
	// just isn't currently set.
	dbEnvKeys() (dbKey, envVar string)
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

// envOnlyError reports that a setting has no dbKey — it's env-only by
// design (see the dbKey="" convention documented on Settings) — and so can
// never be written through Set/Reset regardless of caller.
func envOnlyError(envVar string) error {
	return fmt.Errorf("settings: %s is env-only and cannot be set or reset through this interface", envVar)
}

// Validate reports whether v would be accepted by Set, without persisting
// or mutating anything.
func (s *simpleSetting[T]) Validate(v T) error {
	if s.dbKey == "" {
		return envOnlyError(s.envVar)
	}
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot set %s, overridden by %s", s.dbKey, s.envVar)
	}

	raw := fmt.Sprintf("%v", v)
	parsed, err := s.parse(raw)
	if err != nil {
		return fmt.Errorf("settings: invalid value for %s=%q: %w", s.dbKey, raw, err)
	}

	if s.validate != nil {
		if err := s.validate(parsed); err != nil {
			return fmt.Errorf("settings: invalid value for %s: %w", s.dbKey, err)
		}
	}
	return nil
}

// Set saves a new value to the store.
func (s *simpleSetting[T]) Set(ctx context.Context, v T) error {
	if err := s.Validate(v); err != nil {
		return err
	}

	raw := fmt.Sprintf("%v", v)
	parsed, _ := s.parse(raw) //nolint:errcheck // Validate above already confirmed this parses cleanly

	// Persist before changing the in-memory value: if SaveSetting fails
	// (busy DB, full disk, canceled context), Get() must keep returning the
	// old value, matching what's actually in the store, rather than a value
	// the API just reported as failed to save.
	if err := s.store.SaveSetting(ctx, s.dbKey, raw); err != nil {
		return err
	}

	s.mu.Lock()
	s.value = parsed
	s.mu.Unlock()

	s.fireCallbacks(parsed)
	return nil
}

// Validate reports whether v would be accepted by Set, without persisting
// or mutating anything.
func (s *complexSetting[T]) Validate(v string) error {
	if s.dbKey == "" {
		return envOnlyError(s.envVar)
	}
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot set %s, overridden by %s", s.dbKey, s.envVar)
	}

	parsed, err := s.parse(v)
	if err != nil {
		return fmt.Errorf("settings: invalid value for %s=%q: %w", s.dbKey, v, err)
	}

	if s.validate != nil {
		if err := s.validate(parsed); err != nil {
			return fmt.Errorf("settings: invalid value for %s: %w", s.dbKey, err)
		}
	}
	return nil
}

// Set saves a new value to the store.
func (s *complexSetting[T]) Set(ctx context.Context, v string) error {
	if err := s.Validate(v); err != nil {
		return err
	}
	parsed, _ := s.parse(v) //nolint:errcheck // Validate above already confirmed this parses cleanly

	// Persist before changing the in-memory value — see simpleSetting.Set.
	if err := s.store.SaveSetting(ctx, s.dbKey, v); err != nil {
		return err
	}

	s.mu.Lock()
	s.value = parsed
	s.mu.Unlock()

	s.fireCallbacks(parsed)
	return nil
}

// Reset deletes the stored database value and reverts to the default.
func (s *simpleSetting[T]) Reset(ctx context.Context) error {
	if s.dbKey == "" {
		return envOnlyError(s.envVar)
	}
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot reset %s, overridden by %s", s.dbKey, s.envVar)
	}

	// Persist before changing the in-memory value — see simpleSetting.Set.
	if err := s.store.DeleteSetting(ctx, s.dbKey); err != nil {
		return err
	}

	s.mu.Lock()
	s.value = s.defaultVal
	s.mu.Unlock()

	s.fireCallbacks(s.defaultVal)
	return nil
}

// Reset deletes the stored database value and reverts to parse(defaultJSON).
func (s *complexSetting[T]) Reset(ctx context.Context) error {
	if s.dbKey == "" {
		return envOnlyError(s.envVar)
	}
	if s.envVar != "" && os.Getenv(s.envVar) != "" {
		return fmt.Errorf("settings: cannot reset %s, overridden by %s", s.dbKey, s.envVar)
	}

	defaultParsed, err := s.parse(s.defaultVal)
	if err != nil {
		return fmt.Errorf("settings: default value %q for %s does not parse: %w", s.defaultVal, s.dbKey, err)
	}

	// Persist before changing the in-memory value — see simpleSetting.Set.
	if err := s.store.DeleteSetting(ctx, s.dbKey); err != nil {
		return err
	}

	s.mu.Lock()
	s.value = defaultParsed
	s.mu.Unlock()

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

// dbEnvKeys returns the storage/env keys, for introspection only.
func (s *simpleSetting[T]) dbEnvKeys() (string, string) { return s.dbKey, s.envVar }

// dbEnvKeys returns the storage/env keys, for introspection only.
func (s *complexSetting[T]) dbEnvKeys() (string, string) { return s.dbKey, s.envVar }

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

// parseUint16 parses a 16-bit unsigned integer string (e.g. TCP/UDP ports).
// ParseUint's bitSize=16 already rejects anything outside 0-65535.
func parseUint16(s string) (uint16, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 16)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

// parseUint32 parses a 32-bit unsigned integer string (e.g. 4-byte BGP ASNs).
// ParseUint's bitSize=32 already rejects anything outside 0-4294967295.
func parseUint32(s string) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}
