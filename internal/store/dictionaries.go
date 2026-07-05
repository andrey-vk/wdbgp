package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/netip"
	"strings"
)

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("WARNING: rows close: %v", err)
	}
}

// CatalogEntry is one feed row in its external, name-based form. The store
// maps names and CIDRs onto the categories/services/prefixes dictionaries
// internally; callers never deal in dictionary ids.
type CatalogEntry struct {
	Category string
	Service  string
	CIDR     string
}

// execQueryer covers *sql.Tx and *sql.DB.
type execQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// EnsureCategoryID returns the dictionary id for a category name, creating
// the row if needed. Dictionary rows are append-only: a name referenced by
// a selection or community may not exist in any synced feed yet.
func EnsureCategoryID(ctx context.Context, q execQueryer, name string) (int64, error) {
	if _, err := q.ExecContext(ctx,
		"INSERT OR IGNORE INTO categories(name) VALUES (?)", name); err != nil {
		return 0, err
	}
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM categories WHERE name = ?", name).Scan(&id)
	return id, err
}

// EnsureServiceID returns the dictionary id for a (category, service) name
// pair, creating both rows if needed.
func EnsureServiceID(ctx context.Context, q execQueryer, category, service string) (int64, error) {
	categoryID, err := EnsureCategoryID(ctx, q, category)
	if err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx,
		"INSERT OR IGNORE INTO services(category_id, name) VALUES (?, ?)", categoryID, service); err != nil {
		return 0, err
	}
	var id int64
	err = q.QueryRowContext(ctx,
		"SELECT id FROM services WHERE category_id = ? AND name = ?", categoryID, service).Scan(&id)
	return id, err
}

// entryInsertBatchSize caps rows per multi-row INSERT. 500 rows x 3 bound
// params stays far under modernc.org/sqlite's SQLITE_MAX_VARIABLE_NUMBER
// (32766); prefix resolution binds 2 params per row, same headroom.
const entryInsertBatchSize = 500

// ReplaceCatalogEntries deletes a feed's catalog entries and inserts the
// given ones, resolving names and CIDRs through the dictionaries in
// batches. Invalid CIDRs are rejected. Duplicate rows (including rows that
// only become duplicates after masking) collapse silently, matching the
// set semantics of the entries table.
func ReplaceCatalogEntries(ctx context.Context, tx *sql.Tx, feedID int64, entries []CatalogEntry) error {
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM catalog_entries WHERE feed_id = ?", feedID); err != nil {
		return err
	}
	return AppendCatalogEntries(ctx, tx, feedID, entries)
}

// AppendCatalogEntries inserts catalog entries for a feed without touching
// its existing rows — ReplaceCatalogEntries' non-destructive counterpart.
func AppendCatalogEntries(ctx context.Context, tx *sql.Tx, feedID int64, entries []CatalogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	serviceIDs := map[ServiceKey]int64{}
	for _, entry := range entries {
		key := ServiceKey{Category: entry.Category, Service: entry.Service}
		if _, ok := serviceIDs[key]; ok {
			continue
		}
		id, err := EnsureServiceID(ctx, tx, entry.Category, entry.Service)
		if err != nil {
			return fmt.Errorf("resolve service %q/%q: %w", entry.Category, entry.Service, err)
		}
		serviceIDs[key] = id
	}

	prefixes := make([]netip.Prefix, 0, len(entries))
	parsed := make([]netip.Prefix, len(entries))
	seen := map[netip.Prefix]struct{}{}
	for i, entry := range entries {
		prefix, err := netip.ParsePrefix(entry.CIDR)
		if err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", entry.CIDR, err)
		}
		prefix = prefix.Masked()
		parsed[i] = prefix
		if _, ok := seen[prefix]; !ok {
			seen[prefix] = struct{}{}
			prefixes = append(prefixes, prefix)
		}
	}
	prefixIDs, err := ensurePrefixIDs(ctx, tx, prefixes)
	if err != nil {
		return err
	}

	for start := 0; start < len(entries); start += entryInsertBatchSize {
		end := min(start+entryInsertBatchSize, len(entries))
		chunk := entries[start:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*3)
		for i, entry := range chunk {
			placeholders[i] = "(?, ?, ?)"
			args = append(args,
				feedID,
				serviceIDs[ServiceKey{Category: entry.Category, Service: entry.Service}],
				prefixIDs[parsed[start+i]])
		}
		//nolint:gosec // G202: concatenated part is "(?, ?, ?)" placeholders only, all values bound
		query := "INSERT OR IGNORE INTO catalog_entries(feed_id, service_id, prefix_id) VALUES " +
			strings.Join(placeholders, ",")
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

// InsertCatalogEntries appends entries for a feed in its own transaction —
// a convenience wrapper around AppendCatalogEntries.
func (s *Store) InsertCatalogEntries(ctx context.Context, feedID int64, entries []CatalogEntry) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		return AppendCatalogEntries(ctx, tx, feedID, entries)
	})
}

// ensurePrefixIDs upserts the given (already masked) prefixes into the
// prefixes dictionary and returns their ids, in batches.
func ensurePrefixIDs(ctx context.Context, tx *sql.Tx, prefixes []netip.Prefix) (map[netip.Prefix]int64, error) {
	ids := make(map[netip.Prefix]int64, len(prefixes))
	for start := 0; start < len(prefixes); start += entryInsertBatchSize {
		end := min(start+entryInsertBatchSize, len(prefixes))
		chunk := prefixes[start:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*2)
		for i, prefix := range chunk {
			placeholders[i] = "(?, ?)"
			ip, bits := EncodePrefix(prefix)
			args = append(args, ip, bits)
		}
		values := strings.Join(placeholders, ",")
		//nolint:gosec // G202: concatenated part is "(?, ?)" placeholders only, all values bound
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO prefixes(ip, bits) VALUES "+values, args...); err != nil {
			return nil, err
		}
		//nolint:gosec // G202: concatenated part is "(?, ?)" placeholders only, all values bound
		rows, err := tx.QueryContext(ctx,
			"SELECT id, ip, bits FROM prefixes WHERE (ip, bits) IN (VALUES "+values+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var ip []byte
			var bits int
			if err := rows.Scan(&id, &ip, &bits); err != nil {
				closeRows(rows)
				return nil, err
			}
			prefix, err := DecodePrefix(ip, bits)
			if err != nil {
				closeRows(rows)
				return nil, err
			}
			ids[prefix] = id
		}
		if err := rows.Err(); err != nil {
			closeRows(rows)
			return nil, err
		}
		closeRows(rows)
	}
	for _, prefix := range prefixes {
		if _, ok := ids[prefix]; !ok {
			return nil, fmt.Errorf("prefix %s missing after upsert", prefix)
		}
	}
	return ids, nil
}

// PruneOrphanPrefixes deletes dictionary prefixes no longer referenced by
// any catalog entry. Run after a feed resync (outside its transaction):
// resync replaces a feed's entries wholesale, so prefixes that dropped out
// of the feed would otherwise accumulate forever.
func (s *Store) PruneOrphanPrefixes(ctx context.Context) (int64, error) {
	result, err := s.DB.ExecContext(ctx,
		"DELETE FROM prefixes WHERE id NOT IN (SELECT prefix_id FROM catalog_entries)")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
