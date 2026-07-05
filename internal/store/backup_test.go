package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/settings"

	_ "modernc.org/sqlite"
)

func TestBackupConfigDefaults(t *testing.T) {
	t.Setenv("WDBGP_BACKUP_ENABLED", "")
	t.Setenv("WDBGP_BACKUP_DIR", "")
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	t.Setenv("WDBGP_DB", dbPath)

	s, err := settings.New(settings.NewTestStore())
	if err != nil {
		t.Fatalf("settings.New() error: %v", err)
	}
	if !s.BackupEnabled.Get() {
		t.Error("BackupEnabled default should be true")
	}
	if s.BackupDir.Get() != tmpDir {
		t.Errorf("BackupDir = %q, want %q", s.BackupDir.Get(), tmpDir)
	}
}

func TestBackupCreatedWhenMigrationsPending(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "backups")

	// Step 1: Create a pre-existing DB with user data.
	{
		s, err := Open(dbPath, false, "", false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(context.Background(), User{
			Name: "test-user", PeerIP: "192.0.2.1", PeerASN: 65001,
			Enabled:  true,
			Networks: []string{"10.0.0.0/8"},
		})
		if err != nil {
			s.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		s.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 2: Simulate an upgrade — remove the last migration to create pending work.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)"); err != nil {
			db.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 3: Reopen with backup enabled — migrations are pending → backup created.
	s, err := Open(dbPath, true, backupDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup file, got %d: %v", len(entries), entries)
	}
	if !strings.HasSuffix(entries[0].Name(), ".sqlite3") {
		t.Fatalf("backup file missing .sqlite3 extension: %s", entries[0].Name())
	}
	backupPath := filepath.Join(backupDir, entries[0].Name())

	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}

	// Verify backup can be opened and contains pre-migration data.
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close() //nolint:errcheck // test cleanup

	var userCount int
	if err := backupDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 1 {
		t.Errorf("backup should have 1 user (pre-migration data), got %d", userCount)
	}
}

func TestBackupNotCreatedWhenUpToDate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "backups")

	// Step 1: Create a DB with migrations applied (disable backup so no backup now).
	{
		s, err := Open(dbPath, false, "", false)
		if err != nil {
			t.Fatal(err)
		}
		s.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 2: Reopen with backup enabled — all migrations already applied,
	// so the "len(applied) < len(migrations)" check is false → no backup.
	s, err := Open(dbPath, true, backupDir, false)
	if err != nil {
		t.Fatal(err)
	}
	s.Close() //nolint:errcheck,gosec // test cleanup

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		// Dir might not exist at all when no backup was created — that's fine.
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sqlite3") {
			t.Fatalf("unexpected backup when already up-to-date: %s", e.Name())
		}
	}
}

func TestBackupNotCreatedWhenDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "backups")

	s, err := Open(dbPath, false, backupDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	entries, err := os.ReadDir(backupDir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sqlite3") {
			t.Fatalf("unexpected backup file when disabled: %s", e.Name())
		}
	}
}

func TestBackupNotCreatedOnFreshInstall(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "backups")

	// Fresh install — no existing data, no backups needed.
	s, err := Open(dbPath, true, backupDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	entries, err := os.ReadDir(backupDir)
	if os.IsNotExist(err) {
		return // dir not created — correct, no backup
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sqlite3") {
			t.Fatalf("unexpected backup on fresh install: %s", e.Name())
		}
	}
}

func TestBackupDirAutoCreated(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "non-existent", "backups")

	// Step 1: Create a DB with all migrations applied.
	{
		s, err := Open(dbPath, false, "", false)
		if err != nil {
			t.Fatal(err)
		}
		s.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 2: Simulate an upgrade by removing the last migration record.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)"); err != nil {
			db.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 3: Reopen with backup enabled + non-existent backup dir.
	// Pending migrations trigger backup → dir must be auto-created.
	s, err := Open(dbPath, true, backupDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	info, err := os.Stat(backupDir)
	if err != nil {
		t.Fatalf("backup dir was not auto-created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("backup path is not a directory")
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	hasSQLite := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sqlite3") {
			hasSQLite = true
			break
		}
	}
	if !hasSQLite {
		t.Fatal("no .sqlite3 backup file found in auto-created directory")
	}
}

func TestAutoRestoreEnvDefaultFalse(t *testing.T) {
	t.Setenv("WDBGP_AUTO_RESTORE_ENABLED", "")
	tmpDir := t.TempDir()
	t.Setenv("WDBGP_DB", filepath.Join(tmpDir, "test.sqlite3"))
	s, err := settings.New(settings.NewTestStore())
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoRestoreEnabled.Get() {
		t.Error("AutoRestoreEnabled should be false when env is unset")
	}
}

func TestRestoreFromBackup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "backups")
	ctx := context.Background()

	// Step 1: Create a DB, add a user, close.
	{
		s, err := Open(dbPath, false, "", false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(ctx, User{
			Name: "restore-user", PeerIP: "10.0.1.1", PeerASN: 65100,
			Enabled:  true,
			Networks: []string{"192.168.1.0/24"},
		})
		if err != nil {
			s.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		s.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 2: Take a manual backup.
	if err := os.MkdirAll(backupDir, 0755); err != nil { //nolint:gosec // container filesystem, test dir
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, "wdbgp-backup-20240101000000.sqlite3")
	{
		src, err := os.Open(dbPath) //nolint:gosec // test file, controlled path
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Create(backupPath) //nolint:gosec // test file, controlled path
		if err != nil {
			src.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		if _, err := dst.ReadFrom(src); err != nil {
			src.Close() //nolint:errcheck,gosec // test cleanup
			dst.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		src.Close() //nolint:errcheck,gosec // test cleanup
		dst.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 3: Add a fake higher migration to simulate "newer version".
	extraVersion := migrations[len(migrations)-1].Version + 1
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'future', 'now')`, extraVersion); err != nil {
			db.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Step 4: Reopen with auto-restore enabled.
	s, err := Open(dbPath, false, backupDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	// Verify restored DB is not degraded.
	if s.Degraded {
		t.Fatal("restored DB should not be degraded")
	}
	var version int
	if err := s.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != migrations[len(migrations)-1].Version {
		t.Fatalf("restored DB version = %d, want %d", version, migrations[len(migrations)-1].Version)
	}

	// Verify restored DB has 1 user.
	users, err := s.Users(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("restored DB should have 1 user, got %d", len(users))
	}

	// Verify old DB was saved.
	incompatiblePath := strings.TrimSuffix(dbPath, ".sqlite3") + ".incompatible-v" + strconv.Itoa(extraVersion) + ".sqlite3"
	if _, err := os.Stat(incompatiblePath); err != nil {
		t.Fatalf("incompatible save file not found at %s: %v", incompatiblePath, err)
	}
}

func TestDegradedWhenAutoRestoreDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	ctx := context.Background()

	{
		s, err := Open(dbPath, false, "", false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(ctx, User{
			Name: "degraded-user", PeerIP: "10.0.2.1", PeerASN: 65100,
			Enabled:  true,
			Networks: []string{"192.168.2.0/24"},
		})
		if err != nil {
			s.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		s.Close() //nolint:errcheck,gosec // test cleanup
	}

	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'future', 'now')`, migrations[len(migrations)-1].Version+1); err != nil {
			db.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck,gosec // test cleanup
	}

	s, err := Open(dbPath, false, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	if !s.Degraded {
		t.Fatal("Store.Degraded should be true")
	}
	if s.DBVersion != migrations[len(migrations)-1].Version+1 {
		t.Fatalf("DBVersion = %d, want %d", s.DBVersion, migrations[len(migrations)-1].Version+1)
	}
	if !strings.Contains(s.DegradedReason, "auto-restore disabled") {
		t.Fatalf("DegradedReason = %q, want to contain 'auto-restore disabled'", s.DegradedReason)
	}
}

func TestDegradedWhenNoBackupFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "empty-backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil { //nolint:gosec // container filesystem, test dir
		t.Fatal(err)
	}
	ctx := context.Background()

	{
		s, err := Open(dbPath, false, "", false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(ctx, User{
			Name: "no-backup-user", PeerIP: "10.0.3.1", PeerASN: 65100,
			Enabled:  true,
			Networks: []string{"192.168.3.0/24"},
		})
		if err != nil {
			s.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		s.Close() //nolint:errcheck,gosec // test cleanup
	}

	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'future', 'now')`, migrations[len(migrations)-1].Version+1); err != nil {
			db.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck,gosec // test cleanup
	}

	s, err := Open(dbPath, false, backupDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	if !s.Degraded {
		t.Fatal("Store.Degraded should be true")
	}
	if !strings.Contains(s.DegradedReason, "no backup") {
		t.Fatalf("DegradedReason = %q, want to contain 'no backup'", s.DegradedReason)
	}
}

// TestDegradedWhenOnlyCorruptedBackupFound covers a backup file whose
// schema_migrations table scans cleanly up through the target version and
// then hits a row that fails to Scan into an int. Before this table's rows
// were scanned in full, tryRestore treated the scan failure as if it were
// "no more rows": the truncated (but coincidentally target-version-matching)
// list of versions scanned so far was accepted as a valid restore candidate,
// silently overwriting the live DB with a corrupted backup instead of
// falling into degraded mode.
func TestDegradedWhenOnlyCorruptedBackupFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "backups")
	ctx := context.Background()
	targetVersion := migrations[len(migrations)-1].Version

	{
		s, err := Open(dbPath, false, "", false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(ctx, User{
			Name: "corrupt-backup-user", PeerIP: "10.0.4.1", PeerASN: 65100,
			Enabled:  true,
			Networks: []string{"192.168.4.0/24"},
		})
		if err != nil {
			s.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		s.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Simulate an upgrade: the live DB now claims a version newer than the
	// server knows about, forcing the auto-restore path on reopen.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'future', 'now')`, targetVersion+1); err != nil {
			db.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		db.Close() //nolint:errcheck,gosec // test cleanup
	}

	// Only backup available is corrupted: its schema_migrations rows scan
	// cleanly for versions 1..targetVersion, then hit a row with a
	// non-numeric value that fails Scan into an int.
	if err := os.MkdirAll(backupDir, 0755); err != nil { //nolint:gosec // container filesystem, test dir
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, "wdbgp-backup-corrupt.sqlite3")
	{
		backupDB, err := sql.Open("sqlite", backupPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := backupDB.Exec(`CREATE TABLE schema_migrations (version INTEGER)`); err != nil {
			backupDB.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		for v := 1; v <= targetVersion; v++ {
			if _, err := backupDB.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, v); err != nil {
				backupDB.Close() //nolint:errcheck,gosec // test cleanup
				t.Fatal(err)
			}
		}
		// SQLite's default type ordering sorts TEXT storage class after
		// INTEGER, so this row scans last, after every legitimate version.
		if _, err := backupDB.Exec(`INSERT INTO schema_migrations(version) VALUES ('corrupted-row')`); err != nil {
			backupDB.Close() //nolint:errcheck,gosec // test cleanup
			t.Fatal(err)
		}
		backupDB.Close() //nolint:errcheck,gosec // test cleanup
	}

	s, err := Open(dbPath, false, backupDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test cleanup

	if !s.Degraded {
		t.Fatal("Store.Degraded should be true — the only backup is corrupted and must not be used for restore")
	}
	if !strings.Contains(s.DegradedReason, "no backup") {
		t.Fatalf("DegradedReason = %q, want to contain 'no backup'", s.DegradedReason)
	}

	// The corrupted backup must not have overwritten the live DB.
	var userCount int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 1 {
		t.Errorf("live DB should be untouched (1 user), got %d", userCount)
	}
}
