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

func TestNewSetting_Bool_NoEnvNoDB(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("expected default false")
	}
	if s.IsEnvSet() {
		t.Error("IsEnvSet should be false")
	}
	if s.HasDBValue() {
		t.Error("HasDBValue should be false")
	}
}

func TestNewSetting_Bool_EnvTrue(t *testing.T) {
	t.Setenv("TEST_BOOL", "true")
	store := newMockStore()
	s, err := newSetting(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
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

func TestNewSetting_Bool_EnvFalse(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	store := newMockStore()
	s, err := newSetting(true, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("expected env value false")
	}
}

func TestNewSetting_Bool_InvalidEnv(t *testing.T) {
	t.Setenv("TEST_BOOL", "yesplease")
	store := newMockStore()
	_, err := newSetting(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err == nil {
		t.Fatal("expected error for invalid env")
	}
}

func TestNewSetting_Bool_DBValue(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{"test_key": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != true {
		t.Error("expected DB value true")
	}
	if !s.HasDBValue() {
		t.Error("HasDBValue should be true")
	}
}

func TestNewSetting_Bool_EnvWinsOverDB(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	store := newMockStore()
	store.settings["test_key"] = "true"
	s, err := newSetting(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("env should win over DB")
	}
	if s.IsEnvSet() != true {
		t.Error("IsEnvSet should be true")
	}
	if s.HasDBValue() {
		t.Error("HasDBValue should be false when env is set")
	}
}

func TestNewSetting_Bool_InvalidDBIgnored(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{"test_key": "notabool"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("expected default after invalid DB value")
	}
	if s.HasDBValue() {
		t.Error("HasDBValue should be false when DB value was invalid")
	}
}

func TestSetting_Bool_Set(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if s.Get() != true {
		t.Error("Get should return true after Set")
	}
	if !s.HasDBValue() {
		t.Error("HasDBValue should be true after Set")
	}
	if store.saved["test_key"] != "true" {
		t.Errorf("store saved %q, want true", store.saved["test_key"])
	}
}

func TestSetting_Bool_SetOnEnv(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	store := newMockStore()
	s, err := newSetting(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(context.Background(), true); err == nil {
		t.Fatal("expected error when setting env-overridden field")
	}
}

func TestSetting_Bool_Reset(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Get() != false {
		t.Error("Get should return default after Reset")
	}
	if s.HasDBValue() {
		t.Error("HasDBValue should be false after Reset")
	}
	if len(store.deleted) != 1 || store.deleted[0] != "test_key" {
		t.Error("store.DeleteSetting should have been called")
	}
}

func TestSetting_Bool_ResetOnEnv(t *testing.T) {
	t.Setenv("TEST_BOOL", "true")
	store := newMockStore()
	s, err := newSetting(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(context.Background()); err == nil {
		t.Fatal("expected error when resetting env-overridden field")
	}
}

func TestSetting_Bool_JSON_Default(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	j := s.JSON()
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

func TestSetting_Bool_JSON_DBSet(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	s.Set(context.Background(), true)
	j := s.JSON()
	if j.Value == nil || *j.Value != true {
		t.Errorf("Value should be true after Set, got %v", j.Value)
	}
	if j.EnvOverride {
		t.Error("EnvOverride should be false")
	}
}

func TestSetting_Bool_JSON_EnvSet(t *testing.T) {
	t.Setenv("TEST_BOOL", "true")
	store := newMockStore()
	s, err := newSetting(false, "test_key", "TEST_BOOL", parseBool, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	j := s.JSON()
	if j.Value == nil || *j.Value != true {
		t.Errorf("Value should be true from env, got %v", j.Value)
	}
	if !j.EnvOverride {
		t.Error("EnvOverride should be true")
	}
}

func TestSetting_Int_Basic(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(42, "test_key", "", parseInt, store, map[string]string{})
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

func TestSetting_Int_Env(t *testing.T) {
	t.Setenv("TEST_INT", "100")
	store := newMockStore()
	s, err := newSetting(42, "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get() != 100 {
		t.Error("expected env 100")
	}
}

func TestSetting_Int_InvalidEnv(t *testing.T) {
	t.Setenv("TEST_INT", "notanumber")
	store := newMockStore()
	_, err := newSetting(42, "test_key", "TEST_INT", parseInt, store, map[string]string{})
	if err == nil {
		t.Fatal("expected error for invalid int env")
	}
}

func TestSetting_String_Basic(t *testing.T) {
	store := newMockStore()
	s, err := newSetting("default", "test_key", "", parseString, store, map[string]string{})
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

func TestSetting_ConcurrentGet(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(42, "test_key", "", parseInt, store, map[string]string{})
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

func TestNewSetting_EmptyEnvVar(t *testing.T) {
	store := newMockStore()
	s, err := newSetting(false, "test_key", "", parseBool, store, map[string]string{})
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
