package migrations

import (
	"context"
	"database/sql"
)

func V010(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM feeds
WHERE (
       name = 'ipranges'
    OR url = 'https://github.com/lord-alfred/ipranges'
    OR url = 'https://github.com/antonme/ipranges'
)
  AND id != (
      SELECT MIN(candidate.id)
      FROM feeds candidate
      WHERE candidate.name = 'ipranges'
         OR candidate.url = 'https://github.com/lord-alfred/ipranges'
         OR candidate.url = 'https://github.com/antonme/ipranges'
  );

UPDATE feeds
SET name = 'ipranges',
    url = 'https://github.com/antonme/ipranges',
    mode_id = 2,
    last_success = NULL,
    last_error = NULL
WHERE name = 'ipranges'
   OR url = 'https://github.com/lord-alfred/ipranges'
   OR url = 'https://github.com/antonme/ipranges';

DELETE FROM catalog_entries
WHERE feed_id = (
    SELECT id FROM feeds
    WHERE name = 'ipranges'
      AND url = 'https://github.com/antonme/ipranges'
);
`)
	return err
}
