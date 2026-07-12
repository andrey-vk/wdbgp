package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestSeedBuiltInAdaptersRenamesCollidingCustomAdapter covers the upgrade
// scenario where an install already has a custom (non-builtin) adapter
// whose name matches a newly-added builtin: the custom adapter must be
// renamed out of the way — not silently reinterpreted as the builtin, and
// not left blocking the builtin from ever being created — and any feed
// using it must keep working unaffected, since feeds reference adapters by
// id, not name.
func TestSeedBuiltInAdaptersRenamesCollidingCustomAdapter(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "collision.sqlite3"), false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	// Remove the builtin ASN adapter seeded by Open, then plant a custom
	// adapter under the same name with a feed pointing at it — simulating
	// a DB from before the ASN builtin existed.
	if _, err := db.DB.ExecContext(ctx, "DELETE FROM feed_adapters WHERE name = 'ASN'"); err != nil {
		t.Fatal(err)
	}
	custom, err := db.AddFeedAdapter(ctx, FeedAdapter{
		Name:   "ASN",
		Source: "function sync(feed, api) { return []; }",
	})
	if err != nil {
		t.Fatal(err)
	}
	feedID, err := db.AddFeed(ctx, "my-asn-feed", "https://example.com", custom.ID, true, 0, "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// The custom adapter must survive under a new name, same id, untouched
	// otherwise.
	renamed, err := db.FeedAdapter(ctx, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name == "ASN" {
		t.Fatal("custom adapter was not renamed out of the way")
	}
	if renamed.BuiltIn {
		t.Fatal("custom adapter must not become builtin")
	}
	if renamed.Source != custom.Source {
		t.Fatalf("custom adapter source changed: got %q", renamed.Source)
	}

	// The feed must still resolve to the same (renamed) adapter row.
	feed, err := db.Feed(ctx, feedID)
	if err != nil {
		t.Fatal(err)
	}
	if feed.AdapterID != custom.ID {
		t.Fatalf("feed adapter_id = %d, want unchanged %d", feed.AdapterID, custom.ID)
	}

	// A genuine builtin ASN adapter must now exist.
	var builtinID int64
	var isBuiltin bool
	if err := db.DB.QueryRowContext(ctx,
		"SELECT id, is_builtin FROM feed_adapters WHERE name = 'ASN'").Scan(&builtinID, &isBuiltin); err != nil {
		t.Fatal(err)
	}
	if !isBuiltin {
		t.Fatal("builtin ASN adapter was not created")
	}
	if builtinID == custom.ID {
		t.Fatal("builtin ASN adapter reused the custom adapter's row")
	}
}
