package settings

import (
	"context"
	"sync"
	"testing"
)

type mockStore struct {
	settings map[string]string
	saved    map[string]string
	deleted  []string
}

func newMockStore() *mockStore {
	return &mockStore{
		settings: make(map[string]string),
		saved:    make(map[string]string),
	}
}

func (m *mockStore) GetAllSettings(_ context.Context) (map[string]string, error) {
	result := make(map[string]string, len(m.settings))
	for k, v := range m.settings {
		result[k] = v
	}
	return result, nil
}

func (m *mockStore) SaveSetting(_ context.Context, key, value string) error {
	m.saved[key] = value
	m.settings[key] = value
	return nil
}

func (m *mockStore) DeleteSetting(_ context.Context, key string) error {
	m.deleted = append(m.deleted, key)
	delete(m.settings, key)
	return nil
}

// =============================================================================
// simpleSetting tests
// =============================================================================

func TestSimpleSetting_New_NoEnvNoDB(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("expected default false")
	}
	if s.IsEnvSet() {
		t.Error("IsEnvSet should be false")
	}
	if s.HasDBValue(context.Background()) {
		t.Error("HasDBValue should be false")
	}
}

func TestSimpleSetting_New_EnvTrue(t *testing.T) {
	t.Setenv("TEST_BOOL", "true")
	store := newMockStore()
	s, err := newSimple(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != true {
		t.Error("expected env value true")
	}
	if !s.IsEnvSet() {
		t.Error("IsEnvSet should be true")
	}
}

func TestSimpleSetting_New_EnvFalse(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	store := newMockStore()
	s, err := newSimple(true, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("expected env value false")
	}
}

func TestSimpleSetting_New_InvalidEnv(t *testing.T) {
	t.Setenv("TEST_BOOL", "yesplease")
	store := newMockStore()
	_, err := newSimple(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err == nil {
		t.Fatal("expected error for invalid env")
	}
}

func TestSimpleSetting_New_DBValue(t *testing.T) {
	store := newMockStore()
	store.settings["test_key"] = "true"
	s, err := newSimple(false, "test_key", "", parseBool, store, map[string]string{"test_key": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != true {
		t.Error("expected DB value true")
	}
	if !s.HasDBValue(context.Background()) {
		t.Error("HasDBValue should be true")
	}
}

func TestSimpleSetting_New_EnvWinsOverDB(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	store := newMockStore()
	store.settings["test_key"] = "true"
	s, err := newSimple(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("env should win over DB")
	}
	if s.IsEnvSet() != true {
		t.Error("IsEnvSet should be true")
	}
}

func TestSimpleSetting_New_InvalidDBIgnored(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(false, "test_key", "", parseBool, store, map[string]string{"test_key": "notabool"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("expected default after invalid DB value")
	}
}

func TestSimpleSetting_GetSetReset(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	// Get
	if s.Get() != false {
		t.Error("expected default false")
	}

	// Set
	if err := s.Set(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if s.Get() != true {
		t.Error("Get should return true after Set")
	}
	if !s.HasDBValue(context.Background()) {
		t.Error("HasDBValue should be true after Set")
	}
	if store.saved["test_key"] != "true" {
		t.Errorf("store saved %q, want true", store.saved["test_key"])
	}

	// Reset
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("Get should return default after Reset")
	}
	if s.HasDBValue(context.Background()) {
		t.Error("HasDBValue should be false after Reset")
	}
	if len(store.deleted) != 1 || store.deleted[0] != "test_key" {
		t.Error("store.DeleteSetting should have been called")
	}
}

func TestSimpleSetting_SetOnEnv(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	store := newMockStore()
	s, err := newSimple(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(context.Background(), true); err == nil {
		t.Fatal("expected error when setting env-overridden field")
	}
}

func TestSimpleSetting_ResetOnEnv(t *testing.T) {
	t.Setenv("TEST_BOOL", "true")
	store := newMockStore()
	s, err := newSimple(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(context.Background()); err == nil {
		t.Fatal("expected error when resetting env-overridden field")
	}
}

func TestSimpleSetting_JSON_Default(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	j := s.JSON(map[string]string{})
	if j.Value != nil {
		t.Errorf("Value should be nil for default, got %v", j.Value)
	}
	if j.DefaultValue != false {
		t.Error("DefaultValue should be false")
	}
	if j.EnvOverride {
		t.Error("EnvOverride should be false")
	}
}

func TestSimpleSetting_JSON_DBSet(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	s.Set(context.Background(), true)
	j := s.JSON(map[string]string{"test_key": "true"})
	if j.Value == nil || *j.Value != true {
		t.Errorf("Value should be true from DB, got %v", j.Value)
	}
	if j.EnvOverride {
		t.Error("EnvOverride should be false")
	}
}

func TestSimpleSetting_JSON_EnvSet(t *testing.T) {
	t.Setenv("TEST_BOOL", "true")
	store := newMockStore()
	s, err := newSimple(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	j := s.JSON(map[string]string{})
	if j.Value == nil || *j.Value != true {
		t.Errorf("Value should be true from env, got %v", j.Value)
	}
	if !j.EnvOverride {
		t.Error("EnvOverride should be true")
	}
}

func TestSimpleSetting_Int_Basic(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(42, "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != 42 {
		t.Error("expected default 42")
	}
	if err := s.Set(context.Background(), 777); err != nil {
		t.Fatal(err)
	}
	if s.Get() != 777 {
		t.Error("expected 777 after Set")
	}
	if store.saved["test_key"] != "777" {
		t.Errorf("store saved %q, want 777", store.saved["test_key"])
	}
}

func TestSimpleSetting_Int_Env(t *testing.T) {
	t.Setenv("TEST_INT", "100")
	store := newMockStore()
	s, err := newSimple(42, "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != 100 {
		t.Error("expected env 100")
	}
}

func TestSimpleSetting_Int_InvalidEnv(t *testing.T) {
	t.Setenv("TEST_INT", "notanumber")
	store := newMockStore()
	_, err := newSimple(42, "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err == nil {
		t.Fatal("expected error for invalid int env")
	}
}

func TestSimpleSetting_String_Basic(t *testing.T) {
	store := newMockStore()
	s, err := newSimple("default", "test_key", "", parseString, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != "default" {
		t.Error("expected default")
	}
	if err := s.Set(context.Background(), "custom"); err != nil {
		t.Fatal(err)
	}
	if s.Get() != "custom" {
		t.Error("expected custom after Set")
	}
	if store.saved["test_key"] != "custom" {
		t.Errorf("store saved %q, want custom", store.saved["test_key"])
	}
}

func TestSimpleSetting_ConcurrentGet(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(42, "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Get()
		}()
	}
	wg.Wait()
}

func TestSimpleSetting_EmptyEnvVar(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("expected default")
	}
	if s.IsEnvSet() {
		t.Error("IsEnvSet should be false when envVar is empty string")
	}
}

func TestSimpleSetting_OnChange(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(5, "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	var got int
	var mu sync.Mutex
	unreg := s.OnChange(func(v int) {
		mu.Lock()
		defer mu.Unlock()
		got = v
	})

	if err := s.Set(context.Background(), 77); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	if got != 77 {
		t.Errorf("callback received %d, want 77", got)
	}
	mu.Unlock()

	// Unregister and set again — callback should not fire
	got = 0
	unreg()
	if err := s.Set(context.Background(), 88); err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Errorf("callback fired after unregister: got %d", got)
	}
}

func TestSimpleSetting_OnChange_Reset(t *testing.T) {
	store := newMockStore()
	s, err := newSimple(5, "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	// Set first, then register callback
	s.Set(context.Background(), 99)

	var got int
	s.OnChange(func(v int) {
		got = v
	})

	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Errorf("callback should receive default 5 on reset, got %d", got)
	}
}

// =============================================================================
// complexSetting tests
// =============================================================================

func TestComplexSetting_New_Default(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("42", "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != 42 {
		t.Errorf("expected parsed default 42, got %v", s.Get())
	}
}

func TestComplexSetting_New_Env(t *testing.T) {
	t.Setenv("TEST_INT", "100")
	store := newMockStore()
	s, err := newComplex("42", "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != 100 {
		t.Errorf("expected env 100, got %v", s.Get())
	}
	if !s.IsEnvSet() {
		t.Error("IsEnvSet should be true")
	}
}

func TestComplexSetting_New_DB(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("42", "test_key", "", parseInt, store, map[string]string{"test_key": "99"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != 99 {
		t.Errorf("expected DB value 99, got %v", s.Get())
	}
}

func TestComplexSetting_New_InvalidDBFallsBack(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("42", "test_key", "", parseInt, store, map[string]string{"test_key": "notanumber"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != 42 {
		t.Errorf("expected fallback to parsed default 42, got %v", s.Get())
	}
}

func TestComplexSetting_New_InvalidEnv(t *testing.T) {
	t.Setenv("TEST_INT", "notanumber")
	store := newMockStore()
	_, err := newComplex("42", "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err == nil {
		t.Fatal("expected error for invalid env")
	}
}

func TestComplexSetting_Set(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("42", "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Set(context.Background(), "99"); err != nil {
		t.Fatal(err)
	}
	if s.Get() != 99 {
		t.Errorf("expected 99 after Set, got %v", s.Get())
	}
	if store.saved["test_key"] != "99" {
		t.Errorf("store saved %q, want 99", store.saved["test_key"])
	}
}

func TestComplexSetting_Set_InvalidValue(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("42", "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Set(context.Background(), "notanumber"); err == nil {
		t.Fatal("expected error for invalid Set")
	}
}

func TestComplexSetting_JSON_RawString(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("hello", "test_key", "", parseString, store, map[string]string{"test_key": "world"})
	if err != nil {
		t.Fatal(err)
	}

	j := s.JSON(map[string]string{"test_key": "world"})
	if j.Value == nil || *j.Value != "world" {
		t.Errorf("Value should be raw string 'world', got %v", j.Value)
	}
	if j.DefaultValue != "hello" {
		t.Errorf("DefaultValue should be 'hello', got %v", j.DefaultValue)
	}
}

func TestComplexSetting_JSON_Env(t *testing.T) {
	t.Setenv("TEST_STR", "from_env")
	store := newMockStore()
	s, err := newComplex("hello", "test_key", "TEST_STR", parseString, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	j := s.JSON(map[string]string{})
	if j.Value == nil || *j.Value != "from_env" {
		t.Errorf("Value should be 'from_env', got %v", j.Value)
	}
	if !j.EnvOverride {
		t.Error("EnvOverride should be true")
	}
}

func TestComplexSetting_Reset(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("42", "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	s.Set(context.Background(), "99")
	if s.Get() != 99 {
		t.Fatal("expected 99 after Set")
	}

	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Get() != 42 {
		t.Errorf("expected parsed default 42 after Reset, got %v", s.Get())
	}
	if len(store.deleted) != 1 || store.deleted[0] != "test_key" {
		t.Error("store.DeleteSetting should have been called")
	}
}

func TestComplexSetting_ResetOnEnv(t *testing.T) {
	t.Setenv("TEST_INT", "100")
	store := newMockStore()
	s, err := newComplex("42", "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(context.Background()); err == nil {
		t.Fatal("expected error when resetting env-overridden complex setting")
	}
}

func TestComplexSetting_SetOnEnv(t *testing.T) {
	t.Setenv("TEST_INT", "100")
	store := newMockStore()
	s, err := newComplex("42", "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(context.Background(), "77"); err == nil {
		t.Fatal("expected error when setting env-overridden complex setting")
	}
}

func TestComplexSetting_OnChange(t *testing.T) {
	store := newMockStore()
	s, err := newComplex("42", "test_key", "", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	var got int
	s.OnChange(func(v int) {
		got = v
	})

	if err := s.Set(context.Background(), "77"); err != nil {
		t.Fatal(err)
	}
	if got != 77 {
		t.Errorf("callback received %d, want 77", got)
	}
}
