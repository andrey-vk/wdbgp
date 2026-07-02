package migrations

import (
	"context"
	"database/sql"
	"log"
	"net/netip"
	"sort"
)

// V030 is a one-time, alpha-stage data cleanup: it removes overlapping
// networks between different users whose web_auth is network/both/any
// (login-mode users' networks are inactive and never considered for IP
// resolution, so they're excluded here too), and who are enabled. A
// disabled user's IP match is discarded by requireUser
// (ipMatch := ipErr == nil && ipUser.Enabled) even if UserByIP's raw SQL
// would otherwise resolve to them, so a disabled user's network can never
// actually win an auth resolution — it must not be allowed to "win" this
// cleanup either and cause an enabled user's broader, still-working
// network to be deleted out from under them.
// It mirrors, at whole-entry granularity, how UserByIP's longest-prefix-
// match already resolves an overlap today: the more specific (longer)
// prefix is kept, and any less-specific network from a different user
// that it overlaps with is deleted outright.
//
// This is NOT a perfect range-preserving reconciliation — if a deleted
// broader network also covered addresses outside the overlap, those
// addresses lose their match entirely rather than falling back to the
// broader (now-deleted) entry. For an exact-duplicate tie (same prefix,
// same length, different users), the lower user_id is kept; SQLite's
// actual unordered scan order — today's real tie-break — isn't something
// a migration can reliably reconstruct, so this is a simple, reproducible
// stand-in rather than a guaranteed match for pre-migration behavior.
//
// Every deletion is logged so an admin can review and manually recreate a
// network if they relied on the deleted entry's non-overlapping portion.
func V030(ctx context.Context, tx *sql.Tx) error {
	if !tableExists(ctx, tx, "user_networks") {
		return nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT un.user_id, un.cidr, u.name
		FROM user_networks un
		JOIN users u ON u.id = un.user_id
		WHERE u.web_auth IN ('network', 'both', 'any') AND u.enabled = 1
	`)
	if err != nil {
		return err
	}

	type entry struct {
		userID int64
		name   string
		cidr   string
		prefix netip.Prefix
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.userID, &e.cidr, &e.name); err != nil {
			if cerr := rows.Close(); cerr != nil {
				log.Printf("WARNING: rows close: %v", cerr)
			}
			return err
		}
		prefix, perr := netip.ParsePrefix(e.cidr)
		if perr != nil {
			continue // unparseable legacy data — nothing to compare, leave as-is
		}
		e.prefix = prefix.Masked()
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].userID < entries[j].userID })

	deleted := make([]bool, len(entries))
	for i := range entries {
		if deleted[i] {
			continue
		}
		for j := i + 1; j < len(entries); j++ {
			if deleted[j] || entries[i].userID == entries[j].userID {
				continue
			}
			if !entries[i].prefix.Overlaps(entries[j].prefix) {
				continue
			}
			winner, loser := i, j
			if entries[j].prefix.Bits() > entries[i].prefix.Bits() {
				winner, loser = j, i
			}
			w, l := entries[winner], entries[loser]
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM user_networks WHERE user_id = ? AND cidr = ?", l.userID, l.cidr); err != nil {
				return err
			}
			log.Printf("migration 30: deleted overlapping network %s from user %q (id=%d) — kept %s owned by user %q (id=%d)",
				l.cidr, l.name, l.userID, w.cidr, w.name, w.userID)
			deleted[loser] = true
			if loser == i {
				break
			}
		}
	}

	return nil
}
