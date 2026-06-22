package store

import (
	"context"
	"database/sql"
)

// AppSetting represents a single row from app_settings.
type AppSetting struct {
	Key       string
	Value     string
	UpdatedAt string
}

// GetAllSettings returns all rows from app_settings as a map[key]value.
func (s *Store) GetAllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT key, value FROM app_settings")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		settings[key] = value
	}
	return settings, rows.Err()
}

// SaveSettings upserts multiple settings in a transaction.
// Each entry in the map is a key→value pair.
func (s *Store) SaveSettings(ctx context.Context, settings map[string]string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		for key, value := range settings {
			if _, err := tx.ExecContext(ctx,
				"INSERT OR REPLACE INTO app_settings(key, value, updated_at) VALUES (?, ?, datetime('now'))",
				key, value); err != nil {
				return err
			}
		}
		return nil
	})
}
