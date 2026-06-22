package migrations

import (
	"context"
	"database/sql"
)

func V009(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE catalog_modes (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1
);
INSERT INTO catalog_modes(id, key, name, enabled) VALUES
    (1, 'opencck', 'OpenCCK', 1),
    (2, 'ipranges', 'IPRanges', 0);

ALTER TABLE feeds ADD COLUMN mode_id INTEGER REFERENCES catalog_modes(id);
UPDATE feeds SET mode_id = 1;
INSERT OR IGNORE INTO catalog_entries(feed_id, category, service, cidr)
SELECT keeper.id, ce.category, ce.service, ce.cidr
FROM catalog_entries ce
JOIN feeds duplicate ON duplicate.id = ce.feed_id
JOIN feeds keeper ON keeper.id = (
    SELECT MIN(candidate.id)
    FROM feeds candidate
    WHERE candidate.name = 'ipranges'
       OR candidate.url = 'https://github.com/lord-alfred/ipranges'
)
WHERE (duplicate.name = 'ipranges'
    OR duplicate.url = 'https://github.com/lord-alfred/ipranges')
  AND duplicate.id != keeper.id;

DELETE FROM feeds
WHERE (name = 'ipranges' OR url = 'https://github.com/lord-alfred/ipranges')
  AND id != (
      SELECT MIN(candidate.id)
      FROM feeds candidate
      WHERE candidate.name = 'ipranges'
         OR candidate.url = 'https://github.com/lord-alfred/ipranges'
  );

INSERT INTO feeds(name, url, enabled, mode_id)
SELECT 'ipranges', 'https://github.com/lord-alfred/ipranges', 1, 2
WHERE NOT EXISTS (
    SELECT 1 FROM feeds
    WHERE name = 'ipranges'
       OR url = 'https://github.com/lord-alfred/ipranges'
);

UPDATE feeds
SET name = 'ipranges',
    url = 'https://github.com/lord-alfred/ipranges',
    mode_id = 2
WHERE name = 'ipranges'
   OR url = 'https://github.com/lord-alfred/ipranges';

ALTER TABLE users ADD COLUMN catalog_mode_id INTEGER REFERENCES catalog_modes(id);
ALTER TABLE users ADD COLUMN catalog_mode_editable INTEGER NOT NULL DEFAULT 0;
UPDATE users SET catalog_mode_id = 1;

ALTER TABLE selected_categories RENAME TO selected_categories_legacy;
CREATE TABLE selected_categories (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode_id INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    PRIMARY KEY (user_id, mode_id, category)
);
INSERT INTO selected_categories(user_id, mode_id, category)
SELECT user_id, 1, category FROM selected_categories_legacy;
DROP TABLE selected_categories_legacy;

ALTER TABLE selected_services RENAME TO selected_services_legacy;
CREATE TABLE selected_services (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode_id INTEGER NOT NULL REFERENCES catalog_modes(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    service TEXT NOT NULL,
    PRIMARY KEY (user_id, mode_id, category, service)
);
INSERT INTO selected_services(user_id, mode_id, category, service)
SELECT user_id, 1, category, service FROM selected_services_legacy;
DROP TABLE selected_services_legacy;

CREATE INDEX idx_selected_categories_user_mode
    ON selected_categories(user_id, mode_id);
CREATE INDEX idx_selected_services_user_mode
    ON selected_services(user_id, mode_id);
CREATE INDEX idx_feeds_mode ON feeds(mode_id);
`)
	return err
}
