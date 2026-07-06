//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	m "github.com/andrey-vk/wdbgp/internal/store/migrations"
	_ "modernc.org/sqlite"
)

func TestMigration34ConvertsTimestampsAndDropsModeKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version-33.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	replayMigrationsBefore(t, db, 34)
	// 2026-07-05T12:00:00Z = 1783252800; sync wrote RFC3339Nano, so the
	// fixture uses a fractional-seconds timestamp on feed 1 and a plain
	// RFC3339 one on a second feed.
	if _, err := db.Exec(`
UPDATE feeds SET last_success = '2026-07-05T12:00:00.123456789Z' WHERE id = 1;
INSERT INTO feeds(id, name, url, adapter_id, last_success)
VALUES (90, 'm34-feed', 'https://example.test/m34', 1, '2026-07-05T12:00:00Z');
INSERT INTO app_settings(key, value, updated_at) VALUES ('m34_probe', 'x', '2026-07-05 12:00:00');
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	const wantEpoch = int64(1783252800)
	assertConverted := func() {
		t.Helper()
		feed1, err := s.Feed(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if feed1.LastSuccess != wantEpoch {
			t.Fatalf("feed 1 last_success = %d, want %d (RFC3339Nano input)", feed1.LastSuccess, wantEpoch)
		}
		feed90, err := s.Feed(ctx, 90)
		if err != nil {
			t.Fatal(err)
		}
		if feed90.LastSuccess != wantEpoch {
			t.Fatalf("feed 90 last_success = %d, want %d (RFC3339 input)", feed90.LastSuccess, wantEpoch)
		}

		var updatedAt int64
		if err := s.DB.QueryRow(
			"SELECT updated_at FROM app_settings WHERE key = 'm34_probe'").Scan(&updatedAt); err != nil {
			t.Fatal(err)
		}
		if updatedAt != wantEpoch {
			t.Fatalf("app_settings.updated_at = %d, want %d", updatedAt, wantEpoch)
		}

		var hasKey int
		if err := s.DB.QueryRow(
			"SELECT COUNT(*) FROM pragma_table_info('catalog_modes') WHERE name='key'").Scan(&hasKey); err != nil {
			t.Fatal(err)
		}
		if hasKey != 0 {
			t.Fatal("catalog_modes.key still present after migration 34")
		}
		modes, err := s.CatalogModes(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(modes) != 3 || modes[0].Name != "OpenCCK" {
			t.Fatalf("catalog modes after rebuild = %#v", modes)
		}
	}

	assertConverted()

	// Re-running on an already-converted database must be a clean no-op.
	if err := m.V034NoTxSQL(ctx, s.DB); err != nil {
		t.Fatalf("re-run on converted DB: %v", err)
	}
	assertConverted()
}
