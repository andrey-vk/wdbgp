package migrations

import (
	"context"
	"database/sql"
)

func V016(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
DROP INDEX IF EXISTS idx_user_credentials_login;
CREATE UNIQUE INDEX idx_user_credentials_login_unique ON user_credentials(login);
`)
	return err
}
