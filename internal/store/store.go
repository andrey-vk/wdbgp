package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andrey-vk/wdbgp/internal/config"
	"github.com/andrey-vk/wdbgp/internal/retry"

	_ "modernc.org/sqlite" // SQLite driver
)

// migrations slice is defined in migrations.go

var ErrDBTooNew = errors.New("database schema is newer than this server version")

type Store struct {
	DB             *sql.DB
	dbPath         string // DB file path for backup
	backupEnabled  bool
	backupDir      string
	autoRestore    bool   // auto-restore from backup when DB is newer than server
	Degraded       bool   // true when DB is newer and auto-restore didn't run
	DBVersion      int    // current DB schema version (when degraded)
	ServerVersion  int    // expected server schema version (when degraded)
	DegradedReason string // why degraded: "no backup found", "auto-restore disabled", etc.
}

type FeedAdapter struct {
	ID            int64
	Key           string
	Name          string
	Language      string
	APIVersion    int
	Source        string
	Revision      int64
	BuiltIn       bool
	ForkedFrom    int64 // built-in adapter ID this adapter was forked from (0 for built-ins and customs)
	ForkedVersion int64  // version of the built-in at time of fork
}

const (
	DefaultCatalogModeID    int64 = 1
	IPRangesCatalogModeID   int64 = 2
	SingboxSRSCatalogModeID int64 = 3
)

type ServiceKey struct {
	Category string `json:"category"`
	Service  string `json:"service"`
}

type RouteFilters struct {
	Allow []string
	Deny  []string
}

func Open(path string, cfg config.Config) (*Store, error) {
	if parent := filepath.Dir(path); parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // container filesystem, single user
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA cache_size = -2000;
		PRAGMA temp_store = MEMORY;
		PRAGMA busy_timeout = 30000;
	`); err != nil {
		if err := db.Close(); err != nil {
			log.Printf("WARNING: close: %v", err)
		}
		return nil, err
	}
	s := &Store{DB: db, dbPath: path, backupEnabled: cfg.BackupEnabled, backupDir: cfg.BackupDir, autoRestore: cfg.AutoRestoreEnabled}
	if err := s.Migrate(context.Background()); err != nil {
		if err := db.Close(); err != nil {
			log.Printf("WARNING: close: %v", err)
		}
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.DB.Close()
}

func (s *Store) readAppliedMigrations(ctx context.Context) ([]int, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var applied []int
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied = append(applied, version)
	}
	return applied, rows.Err()
}

func (s *Store) tryRestore(ctx context.Context, applied []int) error {
	targetVersion := migrations[len(migrations)-1].Version
	currentVersion := applied[len(applied)-1]

	backupDir := s.backupDir
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return fmt.Errorf("scan backup dir %s: %w", backupDir, err)
	}

	type candidate struct {
		path    string
		version int
		modTime time.Time
	}
	var candidates []candidate
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sqlite3") || !strings.HasPrefix(e.Name(), "wdbgp-backup-") {
			continue
		}
		backupPath := filepath.Join(backupDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		backupDB, err := sql.Open("sqlite", backupPath)
		if err != nil {
			continue
		}
		backupDB.SetMaxOpenConns(1)
		rows, err := backupDB.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
		if err != nil {
			if err := backupDB.Close(); err != nil {
				log.Printf("WARNING: backup close: %v", err)
			}
			continue
		}
		var versions []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				if err := rows.Close(); err != nil {
					log.Printf("WARNING: rows close: %v", err)
				}
				if err := backupDB.Close(); err != nil {
					log.Printf("WARNING: backup close: %v", err)
				}
				continue
			}
			versions = append(versions, v)
		}
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
		if err := backupDB.Close(); err != nil {
			log.Printf("WARNING: backup close: %v", err)
		}
		if len(versions) == 0 {
			continue
		}
		maxVersion := versions[len(versions)-1]
		if maxVersion != targetVersion {
			continue
		}
		candidates = append(candidates, candidate{backupPath, maxVersion, info.ModTime()})
	}

	if len(candidates) == 0 {
		return fmt.Errorf("no backup found matching server version %d in %s", targetVersion, backupDir)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	best := candidates[0]

	// Save current (newer) DB with distinctive name
	savedPath := strings.TrimSuffix(s.dbPath, ".sqlite3") + ".incompatible-v" + strconv.Itoa(currentVersion) + ".sqlite3"
	if err := os.Rename(s.dbPath, savedPath); err != nil {
		return fmt.Errorf("save current DB: %w", err)
	}

	// Copy backup into place
	src, err := os.Open(best.path)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	dst, err := os.Create(s.dbPath)
	if err != nil {
		if err := src.Close(); err != nil {
			log.Printf("WARNING: src close: %v", err)
		}
		return fmt.Errorf("create new DB: %w", err)
	}
	if _, err := dst.ReadFrom(src); err != nil {
		if err := src.Close(); err != nil {
			log.Printf("WARNING: src close: %v", err)
		}
		if err := dst.Close(); err != nil {
			log.Printf("WARNING: dst close: %v", err)
		}
		return fmt.Errorf("copy backup: %w", err)
	}
	if err := src.Close(); err != nil {
		log.Printf("WARNING: src close: %v", err)
	}
	if err := dst.Close(); err != nil {
		log.Printf("WARNING: dst close: %v", err)
	}

	log.Printf("DB auto-restored from %s (saved incompatible DB as %s)", best.path, savedPath)
	return nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	applied, err := s.readAppliedMigrations(ctx)
	if err != nil {
		return err
	}
	// Validate applied migrations against known migrations.
	// The applied list may contain versions from old binaries that no
	// longer have a corresponding migration in the current code.
	// Skip those (they are idempotent DDL already applied).
	appliedIdx := 0
	migIdx := 0
	for appliedIdx < len(applied) && migIdx < len(migrations) {
		aVer := applied[appliedIdx]
		mVer := migrations[migIdx].Version
		if aVer == mVer {
			appliedIdx++
			migIdx++
		} else if aVer < mVer {
			// Extra version from old binary — skip it.
			appliedIdx++
		} else {
			return fmt.Errorf("database migration history has version %d where %d was expected", aVer, mVer)
		}
	}
	// Build applied set for the migration loop below.
	appliedSet := make(map[int]bool, len(applied))
	for _, v := range applied {
		appliedSet[v] = true
	}
	// If there are leftover applied versions that don't match any migration,
	// the DB was created by a newer binary.
	if appliedIdx < len(applied) || (len(applied) > 0 && len(migrations) > 0 && applied[len(applied)-1] > migrations[len(migrations)-1].Version) {
		if s.autoRestore {
			if err := s.tryRestore(ctx, applied); err != nil {
				// Auto-restore failed — enter degraded mode so the UI
				// can show the error with instructions.
				s.Degraded = true
				s.DBVersion = applied[len(applied)-1]
				s.ServerVersion = migrations[len(migrations)-1].Version
				s.DegradedReason = err.Error()
				return nil
			}
			// Re-open the restored DB
			if err := s.DB.Close(); err != nil {
				log.Printf("WARNING: close: %v", err)
			}
			db, err := sql.Open("sqlite", s.dbPath)
			if err != nil {
				return fmt.Errorf("reopen after restore: %w", err)
			}
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(`
				PRAGMA foreign_keys = ON;
				PRAGMA journal_mode = WAL;
				PRAGMA synchronous = NORMAL;
				PRAGMA cache_size = -2000;
				PRAGMA temp_store = MEMORY;
				PRAGMA busy_timeout = 30000;
			`); err != nil {
				if err := db.Close(); err != nil {
					log.Printf("WARNING: close: %v", err)
				}
				return fmt.Errorf("reopen pragmas: %w", err)
			}
			s.DB = db
			// Re-read applied migrations from restored DB
			applied, err = s.readAppliedMigrations(ctx)
			if err != nil {
				return err
			}
		} else {
			s.Degraded = true
			s.DegradedReason = "auto-restore disabled (set WDBGP_AUTO_RESTORE_ENABLED=true)"
			s.DBVersion = applied[len(applied)-1]
			s.ServerVersion = migrations[len(migrations)-1].Version
			return nil
		}
	}

	// Backup DB before running pending migrations.
	// Only backup when there are existing applied migrations — fresh
	// installs have nothing to preserve.
	if s.backupEnabled && len(applied) > 0 && applied[len(applied)-1] < migrations[len(migrations)-1].Version {
		if err := os.MkdirAll(s.backupDir, 0755); err != nil { //nolint:gosec // container filesystem, single user
			return fmt.Errorf("backup: create dir: %w", err)
		}
		backupName := "wdbgp-backup-" + time.Now().UTC().Format("20060102150405") + ".sqlite3"
		backupPath := filepath.Join(s.backupDir, backupName)

		// Checkpoint WAL so the file copy includes all committed changes.
		if _, err := s.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return fmt.Errorf("backup: wal checkpoint: %w", err)
		}

		// Copy the DB file
		src, err := os.Open(s.dbPath)
		if err != nil {
			return fmt.Errorf("backup: open source: %w", err)
		}
		srcInfo, err := src.Stat()
		if err != nil {
			if err := src.Close(); err != nil {
				log.Printf("WARNING: src close: %v", err)
			}
			return fmt.Errorf("backup: stat source: %w", err)
		}
		dst, err := os.Create(backupPath) //nolint:gosec // path from admin config
		if err != nil {
			if err := src.Close(); err != nil {
				log.Printf("WARNING: src close: %v", err)
			}
			return fmt.Errorf("backup: create backup: %w", err)
		}
		if _, err := dst.ReadFrom(src); err != nil {
			if err := src.Close(); err != nil {
				log.Printf("WARNING: src close: %v", err)
			}
			if err := dst.Close(); err != nil {
				log.Printf("WARNING: dst close: %v", err)
			}
			return fmt.Errorf("backup: copy: %w", err)
		}
		if err := src.Close(); err != nil {
			log.Printf("WARNING: src close: %v", err)
		}
		if err := dst.Close(); err != nil {
			log.Printf("WARNING: dst close: %v", err)
		}
		if srcInfo != nil {
			if err := os.Chmod(backupPath, srcInfo.Mode()); err != nil {
				log.Printf("WARNING: chmod backup: %v", err)
			}
		}

		// Strip catalog_entries from backup (recreatable by feed sync)
		backupDB, err := sql.Open("sqlite", backupPath)
		if err != nil {
			return fmt.Errorf("backup: open backup DB: %w", err)
		}
		backupDB.SetMaxOpenConns(1)
		if _, err := backupDB.Exec("DELETE FROM catalog_entries"); err != nil {
			log.Printf("WARNING: backup cleanup DELETE: %v", err)
		}
		if _, err := backupDB.Exec("VACUUM"); err != nil {
			log.Printf("WARNING: backup cleanup VACUUM: %v", err)
		}
		if err := backupDB.Close(); err != nil {
			log.Printf("WARNING: backup close: %v", err)
		}

		log.Printf("DB backup saved to %s", backupPath)
	}

	for _, migration := range migrations {
		if appliedSet[migration.Version] {
			continue
		}
		// Run NoTxSQL BEFORE the transaction so that a failure does not
		// leave the DB in a partial state with the version already committed.
		// NoTxSQL must be idempotent so it is safe to re-run on retry.
		if migration.NoTxSQL != nil {
			if err := migration.NoTxSQL(ctx, s.DB); err != nil {
				return fmt.Errorf("migration %d NoTxSQL (%s): %w", migration.Version, migration.Name, err)
			}
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		err = migration.Func(ctx, tx)
		if err == nil {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)",
				migration.Version, migration.Name, time.Now().UTC().Format(time.RFC3339Nano),
			)
		}
		if err != nil {
			if err := tx.Rollback(); err != nil {
				log.Printf("WARNING: rollback: %v", err)
			}
			return fmt.Errorf("migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	// Migration 20 backfill: ensure UNIQUE(peer_ip, peer_asn) exists (for DBs
	// that were already migrated before the constraint was added to the DDL).
	if _, err := s.DB.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_ip_asn ON users(peer_ip, peer_asn)`,
	); err != nil {
		return fmt.Errorf("users UNIQUE(peer_ip, peer_asn) index: %w", err)
	}
	// Backfill: index for filtering enabled users by catalog mode.
	if _, err := s.DB.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_users_enabled_catalog_mode ON users(enabled, catalog_mode_id)`,
	); err != nil {
		return fmt.Errorf("users enabled+catalog_mode index: %w", err)
	}
	return s.seedBuiltInAdapters(ctx)
}

func NormalizePrefix(value string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return prefix.Masked().String(), nil
}

func (s *Store) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	// Use retry for database transactions (especially useful for SQLite busy errors)
	return retry.Do(ctx, retry.DatabaseConfig,
		func() error {
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if err := fn(tx); err != nil {
				if err := tx.Rollback(); err != nil {
					log.Printf("WARNING: rollback: %v", err)
				}
				return err
			}
			return tx.Commit()
		},
		retry.TransientError,
	)
}

func (s *Store) FeedAdapters(ctx context.Context) ([]FeedAdapter, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, key, name, language, api_version, source, revision, COALESCE(forked_from, 0), forked_version, is_builtin
FROM feed_adapters
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var adapters []FeedAdapter
	for rows.Next() {
		var adapter FeedAdapter
		var isBuiltin int
		if err := rows.Scan(
			&adapter.ID, &adapter.Key, &adapter.Name, &adapter.Language,
			&adapter.APIVersion, &adapter.Source,
			&adapter.Revision, &adapter.ForkedFrom, &adapter.ForkedVersion,
			&isBuiltin,
		); err != nil {
			return nil, err
		}
		adapter.BuiltIn = isBuiltin == 1
		adapters = append(adapters, adapter)
	}
	return adapters, rows.Err()
}

func (s *Store) FeedAdapter(ctx context.Context, id int64) (FeedAdapter, error) {
	var adapter FeedAdapter
	var isBuiltin int
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, key, name, language, api_version, source, revision, COALESCE(forked_from, 0), forked_version, is_builtin
FROM feed_adapters
WHERE id = ?`, id).Scan(
		&adapter.ID, &adapter.Key, &adapter.Name, &adapter.Language,
		&adapter.APIVersion, &adapter.Source,
		&adapter.Revision, &adapter.ForkedFrom, &adapter.ForkedVersion,
		&isBuiltin,
	)
	adapter.BuiltIn = isBuiltin == 1
	return adapter, err
}

func (s *Store) Stats(ctx context.Context) (int, int, int, error) {
	var categories, services, entries int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT category),
		COUNT(DISTINCT service), COUNT(DISTINCT cidr) FROM catalog_entries`).
		Scan(&categories, &services, &entries)
	return categories, services, entries, err
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
