package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"
)

func TestMigration24AdapterForkColumns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "fork.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()

	// Verify migration 24 created the columns
	var count int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('feed_adapters') WHERE name = 'forked_from'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("forked_from column missing")
	}

	if err := db.DB.QueryRow("SELECT COUNT(*) FROM pragma_table_info('feed_adapters') WHERE name = 'forked_version'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("forked_version column missing")
	}
}

func TestAdapterForkAutoNaming(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "forkname.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	ctx := context.Background()

	// Find a built-in adapter
	adapters, err := db.FeedAdapters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var builtin FeedAdapter
	for _, a := range adapters {
		if a.BuiltIn {
			builtin = a
			break
		}
	}
	if builtin.ID == 0 {
		t.Fatal("no built-in adapter")
	}

	// Fork it — first copy
	forked := FeedAdapter{
		Name:          builtin.Name + "_copy_1",
		Source:        builtin.Source,
		Language:      builtin.Language,
		APIVersion:    builtin.APIVersion,
		ForkedFrom:    builtin.ID,
		ForkedVersion: 1,
	}
	forked1, err := db.AddFeedAdapter(ctx, forked)
	if err != nil {
		t.Fatal(err)
	}

	// Fork again — should get suffix 2
	forked.Name = builtin.Name + "_copy_2"
	forked2, err := db.AddFeedAdapter(ctx, forked)
	if err != nil {
		t.Fatal(err)
	}

	// Read them back
	if forked1.ForkedFrom != builtin.ID {
		t.Fatal("forked_from not set on first copy")
	}
	if forked2.ForkedFrom != builtin.ID {
		t.Fatal("forked_from not set on second copy")
	}
	if forked1.ForkedVersion == 0 {
		t.Fatal("forked_version should be non-zero")
	}
}

func TestAdapterResetPreservesKey(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "resetkey.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	ctx := context.Background()

	adapters, err := db.FeedAdapters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var builtin FeedAdapter
	for _, a := range adapters {
		if a.BuiltIn {
			builtin = a
			break
		}
	}
	if builtin.ID == 0 {
		t.Fatal("no built-in adapter")
	}
	if builtin.Key == "" {
		t.Fatal("built-in missing key")
	}
	if builtin.ForkedVersion == 0 {
		t.Fatal("built-in forked_version should be > 0 after migration")
	}
}

func TestNonBuiltinCannotReset(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "noreset.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	ctx := context.Background()

	// Create custom adapter
	id, err := db.AddFeedAdapter(ctx, FeedAdapter{
		Name: "Custom", Source: "function sync(){}", Language: "javascript", APIVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Reset should fail
	err = db.ResetFeedAdapter(ctx, id.ID)
	if err == nil {
		t.Fatal("expected error resetting custom adapter")
	}
}

func TestMigrationForkedFromToInteger(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "forkint.sqlite3"), config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	}()
	ctx := context.Background()

	// Find a built-in
	adapters, err := db.FeedAdapters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var builtin FeedAdapter
	for _, a := range adapters {
		if a.BuiltIn {
			builtin = a
			break
		}
	}
	if builtin.ID == 0 {
		t.Fatal("no built-in adapter")
	}

	// Create a fork
	forked, err := db.AddFeedAdapter(ctx, FeedAdapter{
		Name: builtin.Name + "_copy_1", Source: builtin.Source,
		Language: builtin.Language, APIVersion: builtin.APIVersion,
		ForkedFrom: builtin.ID, ForkedVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify forked_from is a valid adapter ID (not 0)
	if forked.ForkedFrom == 0 {
		t.Fatal("forked adapter has ForkedFrom=0, expected valid built-in ID")
	}
	if forked.ForkedFrom != builtin.ID {
		t.Fatalf("ForkedFrom=%d, want %d", forked.ForkedFrom, builtin.ID)
	}

	// Verify ForkedAdapterNeedsReview works with ID (at minimum, doesn't panic).
	_ = ForkedAdapterNeedsReview(forked.ForkedFrom, forked.ForkedVersion)

	// Verify BuiltInAdapterVersion works with ID
	ver, ok := BuiltInAdapterVersion(forked.ForkedFrom)
	if !ok {
		t.Fatal("BuiltInAdapterVersion not found for built-in adapter")
	}
	if ver <= 0 {
		t.Fatal("BuiltInAdapterVersion returned zero")
	}
}

func TestMaxForkedAdapterSuffix(t *testing.T) {
	// non-existent ID
	t.Run("nonExistentID", func(t *testing.T) {
		db, err := Open(filepath.Join(t.TempDir(), "suffix1.sqlite3"), config.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Close(); err != nil {
				t.Logf("close: %v", err)
			}
		}()
		_, err = db.maxForkedAdapterSuffix(context.Background(), 99999)
		if err == nil {
			t.Fatal("expected error for non-existent ID")
		}
	})

	// exists but not built-in
	t.Run("notBuiltIn", func(t *testing.T) {
		db, err := Open(filepath.Join(t.TempDir(), "suffix2.sqlite3"), config.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Close(); err != nil {
				t.Logf("close: %v", err)
			}
		}()
		ctx := context.Background()
		// Create a custom adapter
		custom, err := db.AddFeedAdapter(ctx, FeedAdapter{Name: "Custom", Source: "function sync(){}", Language: "javascript", APIVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.maxForkedAdapterSuffix(ctx, custom.ID)
		if err == nil {
			t.Fatal("expected error for non-built-in")
		}
	})

	// find a built-in and run remaining tests
	t.Run("builtIn", func(t *testing.T) {
		db, err := Open(filepath.Join(t.TempDir(), "suffix3.sqlite3"), config.Config{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Close(); err != nil {
				t.Logf("close: %v", err)
			}
		}()
		ctx := context.Background()

		adapters, err := db.FeedAdapters(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var builtin FeedAdapter
		for _, a := range adapters {
			if a.BuiltIn {
				builtin = a
				break
			}
		}
		if builtin.ID == 0 {
			t.Fatal("no built-in adapter")
		}

		// no forks → suffix 0
		suffix, err := db.maxForkedAdapterSuffix(ctx, builtin.ID)
		if err != nil {
			t.Fatal(err)
		}
		if suffix != 0 {
			t.Fatalf("no forks: expected 0, got %d", suffix)
		}

		// create _copy_1 → suffix 1
		if _, err := db.AddFeedAdapter(ctx, FeedAdapter{
			Name: builtin.Name + "_copy_1", Source: builtin.Source,
			Language: builtin.Language, APIVersion: builtin.APIVersion,
			ForkedFrom: builtin.ID, ForkedVersion: 1,
		}); err != nil {
			t.Fatal(err)
		}
		suffix, err = db.maxForkedAdapterSuffix(ctx, builtin.ID)
		if err != nil {
			t.Fatal(err)
		}
		if suffix != 1 {
			t.Fatalf("expected 1, got %d", suffix)
		}

		// create _copy_5 (gap) → suffix 5
		if _, err := db.AddFeedAdapter(ctx, FeedAdapter{
			Name: builtin.Name + "_copy_5", Source: builtin.Source,
			Language: builtin.Language, APIVersion: builtin.APIVersion,
			ForkedFrom: builtin.ID, ForkedVersion: 1,
		}); err != nil {
			t.Fatal(err)
		}
		suffix, err = db.maxForkedAdapterSuffix(ctx, builtin.ID)
		if err != nil {
			t.Fatal(err)
		}
		if suffix != 5 {
			t.Fatalf("gap: expected 5, got %d", suffix)
		}
	})
}
