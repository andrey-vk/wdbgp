package migrations

import (
	"context"
	"database/sql"
)

func V014(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE users ADD COLUMN web_auth TEXT NOT NULL DEFAULT 'network';

CREATE TABLE user_credentials (
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    login         TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    PRIMARY KEY (user_id, login)
);
CREATE INDEX idx_user_credentials_login ON user_credentials(login);
`)
	return err
}
