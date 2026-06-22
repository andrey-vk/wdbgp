package migrations

import (
	"context"
	"database/sql"
)

func V011(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE feed_adapters (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL DEFAULT 'javascript'
        CHECK (language = 'javascript'),
    api_version INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT '',
    allowed_hosts TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 1
);

INSERT INTO feed_adapters(id, key, name, allowed_hosts) VALUES
    (1, 'canonical-json', 'Canonical JSON', ''),
    (2, 'opencck', 'OpenCCK', ''),
    (3, 'ipranges', 'IPRanges', 'raw.githubusercontent.com');

ALTER TABLE feeds ADD COLUMN adapter_id INTEGER REFERENCES feed_adapters(id);
UPDATE feeds SET adapter_id = CASE
    WHEN name = 'ipranges' OR url = 'https://github.com/antonme/ipranges' THEN 3
    WHEN url LIKE 'https://iplist.opencck.org/%'
      OR url LIKE 'https://beta.iplist.opencck.org/%' THEN 2
    ELSE 1
END;

CREATE INDEX idx_feeds_adapter ON feeds(adapter_id);
`)
	return err
}
