package migrations

import (
	"context"
	"database/sql"
)

func V005(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE users ADD COLUMN filter_override_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN filter_editable INTEGER NOT NULL DEFAULT 0;

CREATE TABLE global_route_filters (
    action TEXT NOT NULL CHECK (action IN ('allow', 'deny')),
    cidr TEXT NOT NULL,
    PRIMARY KEY (action, cidr)
);
CREATE INDEX idx_global_route_filters_action ON global_route_filters(action);
CREATE TABLE user_route_filters (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action TEXT NOT NULL CHECK (action IN ('allow', 'deny')),
    cidr TEXT NOT NULL,
    PRIMARY KEY (user_id, action, cidr)
);
CREATE INDEX idx_user_route_filters_user ON user_route_filters(user_id);

INSERT INTO global_route_filters(action, cidr) VALUES
    ('deny', '0.0.0.0/8'),
    ('deny', '10.0.0.0/8'),
    ('deny', '100.64.0.0/10'),
    ('deny', '127.0.0.0/8'),
    ('deny', '169.254.0.0/16'),
    ('deny', '172.16.0.0/12'),
    ('deny', '192.0.0.0/24'),
    ('deny', '192.0.2.0/24'),
    ('deny', '192.168.0.0/16'),
    ('deny', '198.18.0.0/15'),
    ('deny', '198.51.100.0/24'),
    ('deny', '203.0.113.0/24'),
    ('deny', '224.0.0.0/4'),
    ('deny', '240.0.0.0/4'),
    ('deny', '::/128'),
    ('deny', '::1/128'),
    ('deny', '2001:db8::/32'),
    ('deny', 'fc00::/7'),
    ('deny', 'fe80::/10'),
    ('deny', 'ff00::/8');
`)
	return err
}
