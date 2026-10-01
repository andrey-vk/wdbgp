package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
)

// Community represents a catalog community assignment.
type Community struct {
	ModeID    int64
	Category  string
	Service   string // empty = group-level
	Community uint32
}

// findFirstFree returns the first integer >= start that is not in used.
func findFirstFree(start uint32, used map[uint32]bool) uint32 {
	for used[start] {
		start++
	}
	return start
}

// AutoCommunity returns the auto-generated community number for a given
// service position within a group. This is a positional estimate used for
// UI display; actual assignment uses findFirstFree. Not valid for a group's
// own (category-level) entry — use AutoGroupCommunity for that, since a
// group's base value has no "+1 for the first service" offset applied to it.
func AutoCommunity(groupIndex int, serviceIndex int) uint32 {
	for serviceIndex >= 9999 {
		groupIndex++
		serviceIndex -= 9999
	}
	groupCommunity := (groupIndex + 1) * 10000
	return uint32(groupCommunity + serviceIndex + 1) //nolint:gosec // community values fit in uint32
}

// AutoGroupCommunity returns the auto-generated community number for a
// category's own group-level entry — the group base itself (e.g. 10000 for
// the first category), same positional-estimate caveat as AutoCommunity.
func AutoGroupCommunity(groupIndex int) uint32 {
	return uint32((groupIndex + 1) * 10000) //nolint:gosec // community values fit in uint32
}

// GetCommunities returns all communities for a mode.
// Map key: category for groups, "category|service" for services.
// service_id = 0 marks a category-wide (group-level) community.
func (s *Store) GetCommunities(ctx context.Context, modeID int64) (map[string]uint32, error) {
	rows, err := communityRows(ctx, s.DB, modeID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]uint32, len(rows))
	for _, row := range rows {
		if row.Service == "" {
			result[row.Category] = row.Community
		} else {
			result[row.Category+"|"+row.Service] = row.Community
		}
	}
	return result, nil
}

// CommunityRows reads a mode's community assignments as structured rows.
// Unlike GetCommunities' map form, the (category, service) pair stays split,
// so callers that diff or key assignments never have to re-parse a
// "category|service" string — category names may legitimately contain "|",
// so that join is not a safe map key (a group named "a|b" and service "b" in
// category "a" would collide).
func (s *Store) CommunityRows(ctx context.Context, modeID int64) ([]Community, error) {
	return communityRows(ctx, s.DB, modeID)
}

func communityRows(ctx context.Context, q queryer, modeID int64) ([]Community, error) {
	rows, err := q.QueryContext(ctx, `
SELECT c.name, COALESCE(sv.name, ''), cc.community
FROM catalog_communities cc
JOIN categories c ON c.id = cc.category_id
LEFT JOIN services sv ON sv.id = cc.service_id
WHERE cc.mode_id = ? ORDER BY c.name, sv.name`,
		modeID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	var result []Community
	for rows.Next() {
		row := Community{ModeID: modeID}
		if err := rows.Scan(&row.Category, &row.Service, &row.Community); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ModeCommunitySnapshot bundles everything the community export needs for
// one mode, all read from a single transaction.
type ModeCommunitySnapshot struct {
	// Catalog maps category -> services, including services of disabled
	// feeds (as CatalogForMode(..., includeDisabled=true) does) so a mode
	// toggle or feed disablement doesn't make an exported assignment vanish.
	Catalog          map[string][]string
	Communities      []Community
	CategoryPrefixV4 map[string]int
	CategoryPrefixV6 map[string]int
	ServicePrefixV4  map[string]map[string]int
	ServicePrefixV6  map[string]map[string]int
}

// ModeCommunitySnapshot reads a mode's catalog shape, community assignments,
// and prefix counts as one consistent snapshot — generating any missing
// assignments first, in the same transaction.
//
// A feed sync publishes its catalog and generates communities for it as two
// separate transactions (see internal/feeds/feeds.go), so reading those
// pieces with separate queries — even with a defensive GenerateCommunities
// call first — leaves a window where a sync's catalog commit lands between
// this snapshot's own generate step and its later reads, and an entry still
// comes back with no assignment. SQLite serializes writers against a
// snapshot already in progress (the sync's commit either lands fully before
// this transaction starts or fully after it, never partway through), so
// doing the generate-then-read entirely inside one transaction is what
// actually closes the window, not just narrows it.
func (s *Store) ModeCommunitySnapshot(ctx context.Context, modeID int64) (ModeCommunitySnapshot, error) {
	var snap ModeCommunitySnapshot
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		snap, err = modeCommunitySnapshotTx(ctx, tx, modeID)
		return err
	})
	return snap, err
}

// modeCommunitySnapshotTx is ModeCommunitySnapshot's per-mode work, against
// an already-open transaction — shared with AllModeCommunitySnapshots, which
// runs it for every mode inside one transaction instead of one per mode.
func modeCommunitySnapshotTx(ctx context.Context, tx *sql.Tx, modeID int64) (ModeCommunitySnapshot, error) {
	var snap ModeCommunitySnapshot
	if _, err := genCommunitiesRuntime(ctx, tx, modeID); err != nil {
		return snap, err
	}
	var err error
	snap.Catalog, err = catalogForMode(ctx, tx, modeID, true)
	if err != nil {
		return snap, err
	}
	snap.Communities, err = communityRows(ctx, tx, modeID)
	if err != nil {
		return snap, err
	}
	snap.CategoryPrefixV4, snap.CategoryPrefixV6, err = categoryPrefixCounts(ctx, tx, modeID)
	if err != nil {
		return snap, err
	}
	snap.ServicePrefixV4, snap.ServicePrefixV6, err = prefixCounts(ctx, tx, modeID)
	return snap, err
}

// AllModeCommunitySnapshots reads the mode list and every mode's community
// snapshot from one shared transaction, unlike calling ModeCommunitySnapshot
// per mode (each of which opens its own transaction): a feed sync whose
// catalog update spans multiple modes could otherwise commit partway through
// a caller's loop, so the document ends up reflecting the update for one
// mode but not yet for another even though the sync published both
// atomically. Reading the mode list inside the same transaction closes the
// same gap for a mode added, removed, or toggled mid-read.
func (s *Store) AllModeCommunitySnapshots(ctx context.Context, enabledOnly bool) ([]CatalogMode, map[int64]ModeCommunitySnapshot, error) {
	var modes []CatalogMode
	snapshots := make(map[int64]ModeCommunitySnapshot)
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		modes, err = catalogModes(ctx, tx, enabledOnly)
		if err != nil {
			return err
		}
		for _, mode := range modes {
			snap, err := modeCommunitySnapshotTx(ctx, tx, mode.ID)
			if err != nil {
				return err
			}
			snapshots[mode.ID] = snap
		}
		return nil
	})
	return modes, snapshots, err
}

// SetCommunity upserts a community. service="" means group-level.
func (s *Store) SetCommunity(ctx context.Context, modeID int64, category, service string, community uint32) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		categoryID, serviceID, err := resolveCommunityKey(ctx, tx, category, service)
		if err != nil {
			return err
		}
		// Check for duplicate community value
		var existing int
		err = tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM catalog_communities
			WHERE mode_id = ? AND community = ? AND NOT (category_id = ? AND service_id = ?)`,
			modeID, community, categoryID, serviceID).Scan(&existing)
		if err != nil {
			return err
		}
		if existing > 0 {
			return fmt.Errorf("community %d is already used by another category or service in this mode", community)
		}
		// Upsert
		_, err = tx.ExecContext(ctx,
			`INSERT INTO catalog_communities(mode_id, category_id, service_id, community) VALUES (?, ?, ?, ?)
ON CONFLICT(mode_id, category_id, service_id) DO UPDATE SET community = excluded.community`,
			modeID, categoryID, serviceID, community)
		return err
	})
}

// CommunityChange is one community assignment that a reset would alter.
// Old == 0 means the pair had no assignment before; New == 0 means the reset
// would leave it unassigned (its feed no longer contributes the service).
type CommunityChange struct {
	Category string `json:"category"`
	Service  string `json:"service,omitempty"`
	Old      uint32 `json:"old"`
	New      uint32 `json:"new"`
}

// errResetPreviewDiscard aborts the preview transaction so its trial
// regeneration is rolled back. Deliberately worded to avoid the substrings
// retry.TransientError matches on, so the aborted transaction is never retried.
var errResetPreviewDiscard = errors.New("community reset preview: discard trial")

// ErrCommunityResetStale is returned by ResetCommunities when the digest
// passed to it no longer matches the mode's current state — the operator's
// preview is stale (a feed sync regenerated communities, or another admin
// edited one, after the preview was shown) and must not be applied blindly.
var ErrCommunityResetStale = errors.New("community reset preview is stale")

// PreviewCommunityReset reports how ResetCommunities would renumber a mode,
// without writing anything, plus a digest identifying the exact state this
// preview was computed from. Pass the digest back to ResetCommunities so it
// refuses to apply a renumbering the operator never actually reviewed.
//
// The trial runs the real generator inside a transaction that is then rolled
// back, so the preview cannot drift from what an actual reset would produce
// — the alternative, reimplementing the allocation in memory, would be a
// second copy of findFirstFree to keep in sync. Only pairs whose value
// actually changes are returned in the change list; the digest still covers
// the full before/after state, since a reset restricted to a fully
// unchanged mode would otherwise stay "confirmable" forever regardless of
// what else moved around it.
func (s *Store) PreviewCommunityReset(ctx context.Context, modeID int64) ([]CommunityChange, string, error) {
	var before, after []Community
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		before, err = communityRows(ctx, tx, modeID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM catalog_communities WHERE mode_id = ?", modeID); err != nil {
			return err
		}
		if _, err := genCommunitiesRuntime(ctx, tx, modeID); err != nil {
			return err
		}
		after, err = communityRows(ctx, tx, modeID)
		if err != nil {
			return err
		}
		return errResetPreviewDiscard
	})
	if !errors.Is(err, errResetPreviewDiscard) {
		if err != nil {
			return nil, "", err
		}
		// Committing here would mean the trial regeneration was persisted.
		return nil, "", fmt.Errorf("community reset preview committed unexpectedly")
	}
	return diffCommunities(before, after), communityResetDigest(before, after), nil
}

// communityResetDigest fingerprints a mode's community state as observed
// (before) and as a reset would leave it (after). "after" is fully
// determined by the mode's current catalog shape (which categories/services
// catalog_mode_feeds currently resolves to) — genCommunitiesRuntime assigns
// deterministically in alphabetical order starting from an empty table — so
// this digest changes if either the stored assignments or the catalog itself
// has moved, without a separate query to hash the catalog shape directly.
func communityResetDigest(before, after []Community) string {
	h := sha256.New()
	writeRows := func(rows []Community) {
		for _, row := range rows {
			//nolint:errcheck // hash.Hash.Write never returns an error
			fmt.Fprintf(h, "%s\x00%s\x00%d\n", row.Category, row.Service, row.Community)
		}
		h.Write([]byte("--\n"))
	}
	writeRows(before)
	writeRows(after)
	return hex.EncodeToString(h.Sum(nil))
}

// diffCommunities returns the assignments that differ between two snapshots,
// ordered by category then service.
func diffCommunities(before, after []Community) []CommunityChange {
	type key struct{ category, service string }
	oldByKey := make(map[key]uint32, len(before))
	for _, row := range before {
		oldByKey[key{row.Category, row.Service}] = row.Community
	}
	newByKey := make(map[key]uint32, len(after))
	for _, row := range after {
		newByKey[key{row.Category, row.Service}] = row.Community
	}
	changes := make([]CommunityChange, 0)
	for k, oldValue := range oldByKey {
		if newValue := newByKey[k]; newValue != oldValue {
			changes = append(changes, CommunityChange{
				Category: k.category, Service: k.service, Old: oldValue, New: newValue,
			})
		}
	}
	for k, newValue := range newByKey {
		if _, existed := oldByKey[k]; !existed {
			changes = append(changes, CommunityChange{
				Category: k.category, Service: k.service, Old: 0, New: newValue,
			})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Category == changes[j].Category {
			return changes[i].Service < changes[j].Service
		}
		return changes[i].Category < changes[j].Category
	})
	return changes
}

// ResetCommunities discards a mode's community assignments and regenerates
// them from scratch. This renumbers values that downstream routers may have
// hardcoded in their policies, so callers must confirm intent first — see
// PreviewCommunityReset.
//
// expectedDigest must be the digest PreviewCommunityReset returned for the
// preview the caller is applying. Before committing, this recomputes the
// same digest over what it is about to apply and rejects the whole
// transaction with ErrCommunityResetStale if it does not match — closing the
// window between an operator reviewing a preview and clicking apply, during
// which a feed sync or another admin's edit could otherwise get silently
// renumbered into something never shown to anyone.
func (s *Store) ResetCommunities(ctx context.Context, modeID int64, expectedDigest string) (int, error) {
	var generated int
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		before, err := communityRows(ctx, tx, modeID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM catalog_communities WHERE mode_id = ?", modeID); err != nil {
			return err
		}
		generated, err = genCommunitiesRuntime(ctx, tx, modeID)
		if err != nil {
			return err
		}
		after, err := communityRows(ctx, tx, modeID)
		if err != nil {
			return err
		}
		if communityResetDigest(before, after) != expectedDigest {
			generated = 0
			return ErrCommunityResetStale
		}
		return nil
	})
	return generated, err
}

// DeleteCommunity removes a manual community override.
// After deletion, GenerateCommunities fills the auto value.
func (s *Store) DeleteCommunity(ctx context.Context, modeID int64, category, service string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		categoryID, serviceID, err := resolveCommunityKey(ctx, tx, category, service)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`DELETE FROM catalog_communities WHERE mode_id = ? AND category_id = ? AND service_id = ?`,
			modeID, categoryID, serviceID)
		return err
	})
}

// resolveCommunityKey maps a (category, service) name pair to dictionary
// ids, creating dictionary rows as needed. service="" resolves to the
// group-level sentinel service_id 0.
func resolveCommunityKey(ctx context.Context, tx *sql.Tx, category, service string) (categoryID, serviceID int64, err error) {
	categoryID, err = EnsureCategoryID(ctx, tx, category)
	if err != nil {
		return 0, 0, err
	}
	if service == "" {
		return categoryID, 0, nil
	}
	serviceID, err = EnsureServiceID(ctx, tx, category, service)
	if err != nil {
		return 0, 0, err
	}
	return categoryID, serviceID, nil
}

// GenerateCommunities fills missing communities for all categories/services in a mode.
// Uses the 10000*gap scheme. Skips categories/services that already have a community.
// Returns count of newly generated communities.
func (s *Store) GenerateCommunities(ctx context.Context, modeID int64) (int, error) {
	var count int
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var err2 error
		count, err2 = genCommunitiesRuntime(ctx, tx, modeID)
		return err2
	})
	return count, err
}

// genCommunitiesRuntime generates communities using catalog_mode_feeds (post-migration-20).
// Used by GenerateCommunities during normal runtime operation. Which
// categories/services already have a community is read once per mode from
// catalog_communities (into keyComm below) — there is no separate
// "existing" pre-check query, since that would just be reading the same
// table under the same mode_id filter a second time in the same transaction.
func genCommunitiesRuntime(ctx context.Context, tx *sql.Tx, modeID int64) (int, error) {
	var modeIDs []int64
	if modeID > 0 {
		modeIDs = []int64{modeID}
	} else {
		modes, err := tx.QueryContext(ctx, "SELECT DISTINCT id FROM catalog_modes ORDER BY id")
		if err != nil {
			return 0, err
		}
		defer func() {
			if err := modes.Close(); err != nil {
				log.Printf("WARNING: modes close: %v", err)
			}
		}()

		for modes.Next() {
			var id int64
			if err := modes.Scan(&id); err != nil {
				return 0, err
			}
			modeIDs = append(modeIDs, id)
		}
		if err := modes.Err(); err != nil {
			return 0, err
		}
	}

	generated := 0
	for _, mid := range modeIDs {
		commRows, err := tx.QueryContext(ctx, `
SELECT c.name, COALESCE(sv.name, ''), cc.community
FROM catalog_communities cc
JOIN categories c ON c.id = cc.category_id
LEFT JOIN services sv ON sv.id = cc.service_id
WHERE cc.mode_id = ? ORDER BY cc.community`,
			mid)
		if err != nil {
			return 0, err
		}
		used := make(map[uint32]bool)
		keyComm := make(map[string]uint32)
		for commRows.Next() {
			var category, service string
			var community uint32
			if err := commRows.Scan(&category, &service, &community); err != nil {
				if err := commRows.Close(); err != nil {
					log.Printf("WARNING: commRows close: %v", err)
				}
				return 0, err
			}
			used[community] = true
			if service == "" {
				keyComm["grp:"+category] = community
			} else {
				keyComm["svc:"+category+"|"+service] = community
			}
		}
		if err := commRows.Err(); err != nil {
			if cerr := commRows.Close(); cerr != nil {
				log.Printf("WARNING: commRows close: %v", cerr)
			}
			return 0, err
		}
		if err := commRows.Close(); err != nil {
			log.Printf("WARNING: commRows close: %v", err)
		}

		// One query for every (category, service) pair in the mode, instead
		// of a DISTINCT category query followed by a per-category DISTINCT
		// service query — the per-category query was an N+1: one extra
		// round trip for every category in the mode.
		// Reads raw entries via include links (not the materialized
		// catalog_mode_entries) so services of currently-disabled feeds
		// keep their communities pre-generated; exclude links never mint
		// communities — exclusion only subtracts prefixes.
		entryRows, err := tx.QueryContext(ctx, `
SELECT DISTINCT c.name, sv.name
FROM catalog_entries ce
JOIN services sv ON sv.id = ce.service_id
JOIN categories c ON c.id = sv.category_id
JOIN catalog_mode_feeds cmf ON cmf.feed_id = ce.feed_id
WHERE cmf.mode_id = ? AND cmf.exclude = 0
ORDER BY c.name, sv.name`, mid)
		if err != nil {
			return 0, err
		}

		var categories []string
		servicesByCategory := make(map[string][]string)
		for entryRows.Next() {
			var category, service string
			if err := entryRows.Scan(&category, &service); err != nil {
				if err := entryRows.Close(); err != nil {
					log.Printf("WARNING: entryRows close: %v", err)
				}
				return generated, err
			}
			// Rows are ordered by category, so all of a category's rows are
			// contiguous — a new category starts whenever it differs from
			// the last one appended.
			if len(categories) == 0 || categories[len(categories)-1] != category {
				categories = append(categories, category)
			}
			servicesByCategory[category] = append(servicesByCategory[category], service)
		}
		if err := entryRows.Err(); err != nil {
			if cerr := entryRows.Close(); cerr != nil {
				log.Printf("WARNING: entryRows close: %v", cerr)
			}
			return generated, err
		}
		if err := entryRows.Close(); err != nil {
			log.Printf("WARNING: entryRows close: %v", err)
		}

		groupIndex := 0
		for _, category := range categories {
			groupKey := "grp:" + category

			groupCommunity, ok := keyComm[groupKey]
			if !ok {
				groupCommunity = findFirstFree(uint32((groupIndex+1)*10000), used)
				categoryID, err := EnsureCategoryID(ctx, tx, category)
				if err != nil {
					return generated, err
				}
				if _, err := tx.ExecContext(ctx,
					"INSERT OR IGNORE INTO catalog_communities(mode_id, category_id, service_id, community) VALUES (?, ?, 0, ?)",
					mid, categoryID, groupCommunity); err != nil {
					return generated, err
				}
				used[groupCommunity] = true
				generated++
			}

			for _, service := range servicesByCategory[category] {
				svcKey := "svc:" + category + "|" + service
				if _, ok := keyComm[svcKey]; ok {
					continue
				}
				svcCommunity := findFirstFree(groupCommunity+1, used)

				categoryID, err := EnsureCategoryID(ctx, tx, category)
				if err != nil {
					return generated, err
				}
				serviceID, err := EnsureServiceID(ctx, tx, category, service)
				if err != nil {
					return generated, err
				}
				if _, err := tx.ExecContext(ctx,
					"INSERT OR IGNORE INTO catalog_communities(mode_id, category_id, service_id, community) VALUES (?, ?, ?, ?)",
					mid, categoryID, serviceID, svcCommunity); err != nil {
					return generated, err
				}
				used[svcCommunity] = true
				generated++
			}
			groupIndex++
		}
	}
	return generated, nil
}
