//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	m "github.com/andrey-vk/wdbgp/internal/store/migrations"
	_ "modernc.org/sqlite"
)

func TestMigration35ConvertsSnapshotJSONToCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "version-34.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	replayMigrationsBefore(t, db, 35)
	recent := time.Now().UTC().Add(-time.Hour).Unix()
	if _, err := db.Exec(`
INSERT INTO feed_snapshots(id, recorded_at, prefixes) VALUES
    (1, ?, '{"1":100,"2":250}'),
    (2, ?, '{"1":110}'),
    (3, ?, 'not-json-at-all');
`, recent-120, recent-60, recent); err != nil {
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

	assertConverted := func() {
		t.Helper()
		snapshots, err := s.GetFeedSnapshots(ctx, 14)
		if err != nil {
			t.Fatal(err)
		}
		// Snapshot 3 held unparseable JSON: its counts are skipped, and
		// since GetFeedSnapshots joins on counts, it disappears entirely.
		if len(snapshots) != 2 {
			t.Fatalf("snapshots = %d, want 2: %+v", len(snapshots), snapshots)
		}
		first, second := snapshots[0], snapshots[1]
		if first.Prefixes[1] != 100 || first.Prefixes[2] != 250 || len(first.Prefixes) != 2 {
			t.Fatalf("first snapshot prefixes = %v", first.Prefixes)
		}
		if second.Prefixes[1] != 110 || len(second.Prefixes) != 1 {
			t.Fatalf("second snapshot prefixes = %v", second.Prefixes)
		}
		var hasJSONColumn int
		if err := s.DB.QueryRow(
			"SELECT COUNT(*) FROM pragma_table_info('feed_snapshots') WHERE name='prefixes'").
			Scan(&hasJSONColumn); err != nil {
			t.Fatal(err)
		}
		if hasJSONColumn != 0 {
			t.Fatal("feed_snapshots.prefixes still present after migration 35")
		}
	}

	assertConverted()

	// Re-running on an already-converted database must be a clean no-op.
	if err := m.V035NoTxSQL(ctx, s.DB); err != nil {
		t.Fatalf("re-run on converted DB: %v", err)
	}
	assertConverted()

	// The write path still round-trips through the new table.
	if err := s.SaveFeedSnapshot(ctx, map[int64]int{1: 120, 7: 9}); err != nil {
		t.Fatal(err)
	}
	snapshots, err := s.GetFeedSnapshots(ctx, 14)
	if err != nil {
		t.Fatal(err)
	}
	last := snapshots[len(snapshots)-1]
	if last.Prefixes[1] != 120 || last.Prefixes[7] != 9 {
		t.Fatalf("saved snapshot prefixes = %v", last.Prefixes)
	}
}
