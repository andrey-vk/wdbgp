package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"strings"

	"github.com/andrey-vk/wdbgp/internal/prefixfilter"
)

// RebuildModeEntries recomputes the materialized merge result for one mode:
// the entries of its enabled include feeds with the union of its enabled
// exclude feeds' prefixes hole-punched out. Untouched prefixes keep their
// dictionary row byte-identical; overlapped ones are split into fragments
// that inherit the entry's service and are interned into prefixes like any
// other row. The whole rebuild is one transaction (delete + insert), so
// readers never observe a half-built mode.
//
// Exclusion is mode-global: an exclude feed's prefixes are subtracted from
// every service in the mode, and its own category/service labels are
// ignored. The heavy subtraction runs here — once per sync per affected
// mode — never on the per-user reconcile path.
func (s *Store) RebuildModeEntries(ctx context.Context, modeID int64) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		return rebuildModeEntriesTx(ctx, tx, modeID)
	})
}

func rebuildModeEntriesTx(ctx context.Context, tx *sql.Tx, modeID int64) error {
	excludes, err := modeExcludePrefixes(ctx, tx, modeID)
	if err != nil {
		return fmt.Errorf("load exclude prefixes for mode %d: %w", modeID, err)
	}

	if _, err := tx.ExecContext(ctx,
		"DELETE FROM catalog_mode_entries WHERE mode_id = ?", modeID); err != nil {
		return err
	}

	set := prefixfilter.NewExcludeSet(excludes)
	if set.Empty() {
		// No excludes: the merge result is a plain copy — stay in SQL.
		_, err := tx.ExecContext(ctx, `
INSERT INTO catalog_mode_entries(mode_id, service_id, prefix_id)
SELECT DISTINCT cmf.mode_id, ce.service_id, ce.prefix_id
FROM catalog_mode_feeds cmf
JOIN feeds f ON f.id = cmf.feed_id AND f.enabled = 1
JOIN catalog_entries ce ON ce.feed_id = cmf.feed_id
WHERE cmf.mode_id = ? AND cmf.exclude = 0`, modeID)
		return err
	}

	type modeEntry struct {
		serviceID int64
		prefixID  int64 // 0 when the prefix was split and needs interning
		prefix    netip.Prefix
	}

	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT ce.service_id, ce.prefix_id, p.ip, p.bits
FROM catalog_mode_feeds cmf
JOIN feeds f ON f.id = cmf.feed_id AND f.enabled = 1
JOIN catalog_entries ce ON ce.feed_id = cmf.feed_id
JOIN prefixes p ON p.id = ce.prefix_id
WHERE cmf.mode_id = ? AND cmf.exclude = 0`, modeID)
	if err != nil {
		return err
	}

	var result []modeEntry
	fragmentSet := map[netip.Prefix]struct{}{}
	var fragments []netip.Prefix
	for rows.Next() {
		var serviceID, prefixID int64
		var ip []byte
		var bits int
		if err := rows.Scan(&serviceID, &prefixID, &ip, &bits); err != nil {
			closeRows(rows)
			return err
		}
		prefix, err := DecodePrefix(ip, bits)
		if err != nil {
			closeRows(rows)
			return err
		}
		pieces, changed := set.Subtract(prefix)
		if !changed {
			result = append(result, modeEntry{serviceID: serviceID, prefixID: prefixID})
			continue
		}
		for _, piece := range pieces {
			result = append(result, modeEntry{serviceID: serviceID, prefix: piece})
			if _, ok := fragmentSet[piece]; !ok {
				fragmentSet[piece] = struct{}{}
				fragments = append(fragments, piece)
			}
		}
	}
	if err := rows.Err(); err != nil {
		closeRows(rows)
		return err
	}
	closeRows(rows)

	fragmentIDs, err := ensurePrefixIDs(ctx, tx, fragments)
	if err != nil {
		return fmt.Errorf("intern split fragments for mode %d: %w", modeID, err)
	}

	for start := 0; start < len(result); start += entryInsertBatchSize {
		end := min(start+entryInsertBatchSize, len(result))
		chunk := result[start:end]

		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*3)
		for i, entry := range chunk {
			placeholders[i] = "(?, ?, ?)"
			prefixID := entry.prefixID
			if prefixID == 0 {
				prefixID = fragmentIDs[entry.prefix]
			}
			args = append(args, modeID, entry.serviceID, prefixID)
		}
		//nolint:gosec // G202: concatenated part is "(?, ?, ?)" placeholders only, all values bound
		query := "INSERT OR IGNORE INTO catalog_mode_entries(mode_id, service_id, prefix_id) VALUES " +
			strings.Join(placeholders, ",")
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

// modeExcludePrefixes returns the union of all prefixes carried by the
// mode's enabled exclude feeds.
func modeExcludePrefixes(ctx context.Context, tx *sql.Tx, modeID int64) ([]netip.Prefix, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT p.ip, p.bits
FROM catalog_mode_feeds cmf
JOIN feeds f ON f.id = cmf.feed_id AND f.enabled = 1
JOIN catalog_entries ce ON ce.feed_id = cmf.feed_id
JOIN prefixes p ON p.id = ce.prefix_id
WHERE cmf.mode_id = ? AND cmf.exclude = 1`, modeID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var prefixes []netip.Prefix
	for rows.Next() {
		var ip []byte
		var bits int
		if err := rows.Scan(&ip, &bits); err != nil {
			return nil, err
		}
		prefix, err := DecodePrefix(ip, bits)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, rows.Err()
}

// RebuildModeEntriesForFeed rebuilds every mode the feed is linked to, in
// either role. Call after anything that changes what the feed contributes:
// a completed sync, enable/disable, or deletion (for deletion, collect the
// mode list before removing the links).
func (s *Store) RebuildModeEntriesForFeed(ctx context.Context, feedID int64) error {
	modeIDs, err := s.FeedModes(ctx, feedID)
	if err != nil {
		return err
	}
	return s.rebuildModes(ctx, modeIDs)
}

// rebuildModes rebuilds the given modes, stopping at the first error.
func (s *Store) rebuildModes(ctx context.Context, modeIDs []int64) error {
	for _, modeID := range modeIDs {
		if err := s.RebuildModeEntries(ctx, modeID); err != nil {
			return fmt.Errorf("rebuild mode %d: %w", modeID, err)
		}
	}
	return nil
}
