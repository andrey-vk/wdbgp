package migrations

import (
	"context"
	"database/sql"
)

func V012(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
-- Optimize DesiredPrefixes query
CREATE INDEX IF NOT EXISTS idx_users_enabled_catalog_mode ON users(enabled, catalog_mode_id);
CREATE INDEX IF NOT EXISTS idx_catalog_entries_feed_category_service ON catalog_entries(feed_id, category, service);
CREATE INDEX IF NOT EXISTS idx_feeds_enabled_mode ON feeds(enabled, mode_id, id);
CREATE INDEX IF NOT EXISTS idx_catalog_modes_enabled ON catalog_modes(enabled);

-- Additional indexes for common queries
CREATE INDEX IF NOT EXISTS idx_selected_categories_user_mode_category ON selected_categories(user_id, mode_id, category);
CREATE INDEX IF NOT EXISTS idx_selected_services_user_mode_category_service ON selected_services(user_id, mode_id, category, service);
`)
	return err
}
