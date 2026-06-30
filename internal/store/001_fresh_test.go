//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateFreshDatabase(t *testing.T) {
	s := openTestStore(t)
	var version int
	if err := s.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != migrations[len(migrations)-1].Version {
		t.Fatalf("schema version = %d, want %d", version, migrations[len(migrations)-1].Version)
	}
	feeds, err := s.Feeds(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 6 {
		t.Fatalf("feed count = %d, want 6", len(feeds))
	}
	adapters, err := s.FeedAdapters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) != 4 {
		t.Fatalf("adapter count = %d, want 4", len(adapters))
	}
	for _, adapter := range adapters {
		if adapter.Source == "" || adapter.Revision != 1 {
			t.Fatalf("built-in adapter was not seeded: %#v", adapter)
		}
	}
	for _, feed := range feeds {
		wantAdapterID := int64(2)
		if feed.Name == "ipranges" {
			wantAdapterID = 3
		}
		if feed.Name == "Russia GeoIP (SRS)" {
			wantAdapterID = 4
		}
		if feed.AdapterID != wantAdapterID {
			t.Fatalf("feed %q adapter = %d, want %d",
				feed.Name, feed.AdapterID, wantAdapterID)
		}
	}
}

func TestResetBuiltInFeedAdapter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	original, err := s.FeedAdapter(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Name = "Changed"
	changed.Source = "function sync() { return []; }\n"
	if err := s.UpdateFeedAdapter(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetFeedAdapter(ctx, changed.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := s.FeedAdapter(ctx, changed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Name != original.Name ||
		reset.Source != original.Source ||
		reset.Revision != original.Revision+2 ||
		!reset.BuiltIn {
		t.Fatalf("reset adapter = %#v, original = %#v", reset, original)
	}

	custom, err := s.AddFeedAdapter(ctx, FeedAdapter{
		Key: "custom", Name: "Custom",
		Source: "function sync() { return []; }\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResetFeedAdapter(ctx, custom.ID); err == nil {
		t.Fatal("custom adapter reset succeeded")
	}
}

func TestRejectNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations VALUES (99, 'future', 'now')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close() //nolint:errcheck,gosec // test cleanup
	if _, err := Open(path, false, "", false); err == nil {
		t.Fatal("Open accepted a newer database schema")
	}
}
