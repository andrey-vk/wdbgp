package settings

import (
	"context"
	"database/sql"
)

// NewTestStore returns a Store implementation suitable for tests.
// All settings start empty (only defaults and env vars apply).
func NewTestStore() Store {
	return &testStore{settings: make(map[string]string)}
}

// NewTestStoreWith returns a Store pre-loaded with the given key-value pairs.
func NewTestStoreWith(settings map[string]string) Store {
	m := make(map[string]string, len(settings))
	for k, v := range settings {
		m[k] = v
	}
	return &testStore{settings: m}
}

type testStore struct {
	settings map[string]string
}

func (m *testStore) GetAllSettings(_ context.Context) (map[string]string, error) {
	result := make(map[string]string, len(m.settings))
	for k, v := range m.settings {
		result[k] = v
	}
	return result, nil
}

func (m *testStore) SaveSetting(_ context.Context, key, value string) error {
	m.settings[key] = value
	return nil
}

func (m *testStore) DeleteSetting(_ context.Context, key string) error {
	delete(m.settings, key)
	return nil
}

// SaveSettingTx/DeleteSettingTx ignore tx — this in-memory fake has no
// real transactions, so they behave exactly like their non-tx siblings.
func (m *testStore) SaveSettingTx(_ context.Context, _ *sql.Tx, key, value string) error {
	m.settings[key] = value
	return nil
}

func (m *testStore) DeleteSettingTx(_ context.Context, _ *sql.Tx, key string) error {
	delete(m.settings, key)
	return nil
}
