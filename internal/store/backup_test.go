package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/andrey-vk/wdbgp/internal/config"

	_ "modernc.org/sqlite"
)

func TestBackupConfigDefaults(t *testing.T) {
	t.Setenv("WDBGP_BACKUP_ENABLED", "")
	t.Setenv("WDBGP_BACKUP_DIR", "")
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	t.Setenv("WDBGP_DB", dbPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.BackupEnabled {
		t.Error("BackupEnabled default should be true")
	}
	if cfg.BackupDir != tmpDir {
		t.Errorf("BackupDir = %q, want %q", cfg.BackupDir, tmpDir)
	}
}

func TestBackupCreatedWhenMigrationsPending(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "backups")

	// Step 1: Create a pre-existing DB with user data.
	{
		s, err := Open(dbPath, config.Config{BackupEnabled: false})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(context.Background(), User{
			Name: "test-user", PeerIP: "192.0.2.1", PeerASN: 65001,
			Enabled:  true,
			Networks: []string{"10.0.0.0/8"},
		})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		s.Close()
	}

	// Step 2: Simulate an upgrade — remove the last migration to create pending work.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)"); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
	}

	// Step 3: Reopen with backup enabled — migrations are pending → backup created.
	s, err := Open(dbPath, config.Config{
		BackupEnabled: true,
		BackupDir:     backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
	defer backupDB.Close()

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
		s, err := Open(dbPath, config.Config{BackupEnabled: false})
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}

	// Step 2: Reopen with backup enabled — all migrations already applied,
	// so the "len(applied) < len(migrations)" check is false → no backup.
	s, err := Open(dbPath, config.Config{
		BackupEnabled: true,
		BackupDir:     backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

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

	s, err := Open(dbPath, config.Config{
		BackupEnabled: false,
		BackupDir:     backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
	s, err := Open(dbPath, config.Config{
		BackupEnabled: true,
		BackupDir:     backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
		s, err := Open(dbPath, config.Config{BackupEnabled: false})
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}

	// Step 2: Simulate an upgrade by removing the last migration record.
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)"); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
	}

	// Step 3: Reopen with backup enabled + non-existent backup dir.
	// Pending migrations trigger backup → dir must be auto-created.
	s, err := Open(dbPath, config.Config{
		BackupEnabled: true,
		BackupDir:     backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoRestoreEnabled {
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
		s, err := Open(dbPath, config.Config{BackupEnabled: false})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(ctx, User{
			Name: "restore-user", PeerIP: "10.0.1.1", PeerASN: 65100,
			Enabled:  true,
			Networks: []string{"192.168.1.0/24"},
		})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		s.Close()
	}

	// Step 2: Take a manual backup.
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, "wdbgp-backup-20240101000000.sqlite3")
	{
		src, err := os.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.Create(backupPath)
		if err != nil {
			src.Close()
			t.Fatal(err)
		}
		if _, err := dst.ReadFrom(src); err != nil {
			src.Close()
			dst.Close()
			t.Fatal(err)
		}
		src.Close()
		dst.Close()
	}

	// Step 3: Add a fake higher migration to simulate "newer version".
	extraVersion := len(migrations) + 1
	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'future', 'now')`, extraVersion); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
	}

	// Step 4: Reopen with auto-restore enabled.
	s, err := Open(dbPath, config.Config{
		AutoRestoreEnabled: true,
		BackupDir:          backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Verify restored DB is not degraded.
	if s.Degraded {
		t.Fatal("restored DB should not be degraded")
	}
	var version int
	if err := s.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("restored DB version = %d, want %d", version, len(migrations))
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
		s, err := Open(dbPath, config.Config{BackupEnabled: false})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(ctx, User{
			Name: "degraded-user", PeerIP: "10.0.2.1", PeerASN: 65100,
			Enabled:  true,
			Networks: []string{"192.168.2.0/24"},
		})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		s.Close()
	}

	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'future', 'now')`, len(migrations)+1); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
	}

	s, err := Open(dbPath, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !s.Degraded {
		t.Fatal("Store.Degraded should be true")
	}
	if s.DBVersion != len(migrations)+1 {
		t.Fatalf("DBVersion = %d, want %d", s.DBVersion, len(migrations)+1)
	}
	if !strings.Contains(s.DegradedReason, "auto-restore disabled") {
		t.Fatalf("DegradedReason = %q, want to contain 'auto-restore disabled'", s.DegradedReason)
	}
}

func TestDegradedWhenNoBackupFound(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.sqlite3")
	backupDir := filepath.Join(tmpDir, "empty-backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	{
		s, err := Open(dbPath, config.Config{BackupEnabled: false})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AddUser(ctx, User{
			Name: "no-backup-user", PeerIP: "10.0.3.1", PeerASN: 65100,
			Enabled:  true,
			Networks: []string{"192.168.3.0/24"},
		})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		s.Close()
	}

	{
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, 'future', 'now')`, len(migrations)+1); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
	}

	s, err := Open(dbPath, config.Config{
		AutoRestoreEnabled: true,
		BackupDir:          backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !s.Degraded {
		t.Fatal("Store.Degraded should be true")
	}
	if !strings.Contains(s.DegradedReason, "no backup") {
		t.Fatalf("DegradedReason = %q, want to contain 'no backup'", s.DegradedReason)
	}
}
