package store

import (
	"context"
	"database/sql"
	"net/netip"
	"sort"
	"strings"
)

// ModeCategoryGrowth lists the prefixes a sync newly announced in a mode
// through one category.
type ModeCategoryGrowth struct {
	ModeID   int64
	Category string
	Prefixes []netip.Prefix
}

type assocKey struct {
	category string
	prefix   netip.Prefix
}

// presence records, per mode and category, which of the relevant prefixes the
// mode's materialized catalog announces through that category.
type presence map[int64]map[string]map[netip.Prefix]bool

// GrowthCheck is the part of a sync's growth computation that must be measured
// around the entry change. It reads only the prefixes that can change, never a
// whole mode: the candidates the sync adds, the prefixes a removal may uncover
// (the descendants of a removed prefix), and the ancestors of both, which
// decide whether a candidate is covered by a broader prefix in its category.
type GrowthCheck struct {
	candidates map[int64]map[string][]netip.Prefix
	relevant   map[int64]map[string][]netip.Prefix
	before     presence
}

// BeginGrowthCheckTx plans the check and measures the state before the feed's
// entries change. It returns nil when the sync changes no associations.
func BeginGrowthCheckTx(ctx context.Context, tx *sql.Tx, feedID int64, diff FeedSyncDiff) (*GrowthCheck, error) {
	if len(diff.addedAssoc) == 0 && len(diff.removedAssoc) == 0 {
		return nil, nil
	}
	modes, err := feedModeIDsTx(ctx, tx, feedID)
	if err != nil {
		return nil, err
	}
	g := &GrowthCheck{
		candidates: map[int64]map[string][]netip.Prefix{},
		relevant:   map[int64]map[string][]netip.Prefix{},
	}
	for _, modeID := range modes {
		cands := map[string]map[netip.Prefix]bool{}
		add := func(category string, p netip.Prefix) {
			// Default routes are never announced, so they never grow a mode.
			if p.Bits() == 0 {
				return
			}
			if cands[category] == nil {
				cands[category] = map[netip.Prefix]bool{}
			}
			cands[category][p] = true
		}
		for _, a := range diff.addedAssoc {
			add(a.category, a.prefix)
		}
		for _, r := range diff.removedAssoc {
			descendants, err := descendantPrefixesTx(ctx, tx, modeID, r.category, r.prefix)
			if err != nil {
				return nil, err
			}
			for _, d := range descendants {
				add(r.category, d)
			}
		}
		g.candidates[modeID] = map[string][]netip.Prefix{}
		g.relevant[modeID] = map[string][]netip.Prefix{}
		for category, set := range cands {
			rel := map[netip.Prefix]bool{}
			for p := range set {
				rel[p] = true
				for _, anc := range ancestorPrefixes(p) {
					rel[anc] = true
				}
			}
			g.candidates[modeID][category] = sortedPrefixes(set)
			g.relevant[modeID][category] = sortedPrefixes(rel)
		}
	}
	g.before, err = measurePresenceTx(ctx, tx, g.relevant)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// FinishTx measures the state after the entry change and returns, per mode and
// category, the candidates that are now announced there and weren't before.
// Coverage is judged on each side separately, so a prefix a broader one covers
// adds no route. A nil check reports no growth.
func (g *GrowthCheck) FinishTx(ctx context.Context, tx *sql.Tx) ([]ModeCategoryGrowth, error) {
	if g == nil {
		return nil, nil
	}
	after, err := measurePresenceTx(ctx, tx, g.relevant)
	if err != nil {
		return nil, err
	}
	var out []ModeCategoryGrowth
	for modeID, categories := range g.candidates {
		for category, candidates := range categories {
			afterSet := normalizeCovered(after[modeID][category])
			beforeSet := normalizeCovered(g.before[modeID][category])
			var added []netip.Prefix
			for _, p := range candidates {
				if afterSet[p] && !beforeSet[p] {
					added = append(added, p)
				}
			}
			if len(added) > 0 {
				out = append(out, ModeCategoryGrowth{ModeID: modeID, Category: category, Prefixes: added})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModeID != out[j].ModeID {
			return out[i].ModeID < out[j].ModeID
		}
		return out[i].Category < out[j].Category
	})
	return out, nil
}

// normalizeCovered drops every prefix a broader prefix in the set covers.
func normalizeCovered(set map[netip.Prefix]bool) map[netip.Prefix]bool {
	out := make(map[netip.Prefix]bool, len(set))
	for p := range set {
		if !covered(p, set) {
			out[p] = true
		}
	}
	return out
}

// covered reports whether a strictly broader prefix in the set contains p.
func covered(p netip.Prefix, set map[netip.Prefix]bool) bool {
	for _, anc := range ancestorPrefixes(p) {
		if set[anc] {
			return true
		}
	}
	return false
}

// ancestorPrefixes lists the strictly broader prefixes containing p, excluding
// the default route (which never announces).
func ancestorPrefixes(p netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for bits := 1; bits < p.Bits(); bits++ {
		out = append(out, netip.PrefixFrom(p.Addr(), bits).Masked())
	}
	return out
}

// lastAddress returns the highest address inside p, as stored bytes.
func lastAddress(p netip.Prefix) []byte {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	return b
}

// descendantPrefixesTx returns the prefixes strictly inside parent that the
// mode announces through category, found by an address-range scan over the
// prefixes table rather than over the mode.
func descendantPrefixesTx(ctx context.Context, tx *sql.Tx, modeID int64, category string, parent netip.Prefix) ([]netip.Prefix, error) {
	start := parent.Masked().Addr().AsSlice()
	end := lastAddress(parent)
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT p.ip, p.bits
FROM prefixes p
JOIN catalog_mode_entries cme ON cme.prefix_id = p.id AND cme.mode_id = ?
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
WHERE c.name = ? AND length(p.ip) = ? AND p.ip >= ? AND p.ip <= ? AND p.bits > ?`,
		modeID, category, len(start), start, end, parent.Bits())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []netip.Prefix
	for rows.Next() {
		var ip []byte
		var bits int
		if err := rows.Scan(&ip, &bits); err != nil {
			return nil, err
		}
		p, err := DecodePrefix(ip, bits)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// measurePresenceTx reports which of the relevant prefixes each mode announces
// through each category. The lookups go by prefix id, served by an index on
// (mode_id, prefix_id), so they cost the size of the relevant set.
func measurePresenceTx(ctx context.Context, tx *sql.Tx, relevant map[int64]map[string][]netip.Prefix) (presence, error) {
	out := presence{}
	for modeID, categories := range relevant {
		out[modeID] = map[string]map[netip.Prefix]bool{}
		union := map[netip.Prefix]bool{}
		for _, list := range categories {
			for _, p := range list {
				union[p] = true
			}
		}
		ids, err := prefixIDs(ctx, tx, sortedPrefixes(union))
		if err != nil {
			return nil, err
		}
		idPrefix := make(map[int64]netip.Prefix, len(ids))
		var idList []int64
		for p, id := range ids {
			idPrefix[id] = p
			idList = append(idList, id)
		}
		for start := 0; start < len(idList); start += lookupChunk {
			end := min(start+lookupChunk, len(idList))
			if err := presenceChunkTx(ctx, tx, modeID, idList[start:end], idPrefix, categories, out[modeID]); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

const lookupChunk = 300

// presenceSQL takes "?" placeholders for the prefix ids, joined in at %s.
const presenceSQL = `
SELECT DISTINCT c.name, cme.prefix_id
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
WHERE cme.mode_id = ? AND cme.prefix_id IN (%s)`

func presenceChunkTx(ctx context.Context, tx *sql.Tx, modeID int64, ids []int64, idPrefix map[int64]netip.Prefix,
	relevant map[string][]netip.Prefix, into map[string]map[netip.Prefix]bool) error {
	args := []any{modeID}
	marks := make([]string, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}
	query := strings.Replace(presenceSQL, "%s", strings.Join(marks, ","), 1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	for rows.Next() {
		var category string
		var id int64
		if err := rows.Scan(&category, &id); err != nil {
			return err
		}
		p := idPrefix[id]
		if !containsPrefix(relevant[category], p) {
			continue
		}
		if into[category] == nil {
			into[category] = map[netip.Prefix]bool{}
		}
		into[category][p] = true
	}
	return rows.Err()
}

// prefixLookupSQL takes "(?, ?)" placeholders for the prefixes, joined in at %s.
const prefixLookupSQL = `SELECT id, ip, bits FROM prefixes WHERE (ip, bits) IN (VALUES %s)`

// prefixIDsTx resolves prefixes to their ids in the prefixes table; prefixes
// the table doesn't hold can't be announced by any mode.
func prefixIDs(ctx context.Context, q queryer, prefixes []netip.Prefix) (map[netip.Prefix]int64, error) {
	out := make(map[netip.Prefix]int64, len(prefixes))
	for start := 0; start < len(prefixes); start += lookupChunk {
		end := min(start+lookupChunk, len(prefixes))
		chunk := prefixes[start:end]
		args := make([]any, 0, len(chunk)*2)
		marks := make([]string, len(chunk))
		for i, p := range chunk {
			ip, bits := EncodePrefix(p)
			marks[i] = "(?, ?)"
			args = append(args, ip, bits)
		}
		query := strings.Replace(prefixLookupSQL, "%s", strings.Join(marks, ","), 1)
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var ip []byte
			var bits int
			if err := rows.Scan(&id, &ip, &bits); err != nil {
				_ = rows.Close() //nolint:errcheck
				return nil, err
			}
			p, err := DecodePrefix(ip, bits)
			if err != nil {
				_ = rows.Close() //nolint:errcheck
				return nil, err
			}
			out[p] = id
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close() //nolint:errcheck
			return nil, err
		}
		_ = rows.Close() //nolint:errcheck
	}
	return out, nil
}

func containsPrefix(list []netip.Prefix, p netip.Prefix) bool {
	for _, q := range list {
		if q == p {
			return true
		}
	}
	return false
}

func sortedPrefixes(set map[netip.Prefix]bool) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// announcedCoverersTx returns which of the given prefixes are covered by a
// strictly broader prefix the user already announces: one carried by a category
// the user has selected in the mode and kept by their route filters. Growth
// covered that way adds no route for the user, even if its own category is new.
func announcedCoverers(ctx context.Context, q queryer, userID, modeID int64, filters RouteFilters, prefixes []netip.Prefix) (map[netip.Prefix]bool, error) {
	ancestors := map[netip.Prefix]bool{}
	for _, p := range prefixes {
		for _, anc := range ancestorPrefixes(p) {
			ancestors[anc] = true
		}
	}
	if len(ancestors) == 0 {
		return map[netip.Prefix]bool{}, nil
	}
	ids, err := prefixIDs(ctx, q, sortedPrefixes(ancestors))
	if err != nil {
		return nil, err
	}
	idPrefix := make(map[int64]netip.Prefix, len(ids))
	var idList []int64
	for p, id := range ids {
		idPrefix[id] = p
		idList = append(idList, id)
	}
	announced := map[netip.Prefix]bool{}
	for start := 0; start < len(idList); start += lookupChunk {
		end := min(start+lookupChunk, len(idList))
		args := []any{userID, modeID}
		marks := make([]string, 0, end-start)
		for _, id := range idList[start:end] {
			marks = append(marks, "?")
			args = append(args, id)
		}
		query := strings.Replace(announcedCoverersSQL, "%s", strings.Join(marks, ","), 1)
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close() //nolint:errcheck
				return nil, err
			}
			announced[idPrefix[id]] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close() //nolint:errcheck
			return nil, err
		}
		_ = rows.Close() //nolint:errcheck
	}
	var announcedList []netip.Prefix
	for p := range announced {
		announcedList = append(announcedList, p)
	}
	kept, err := applyRouteFiltersToPrefixes(announcedList, filters)
	if err != nil {
		return nil, err
	}
	keptSet := make(map[netip.Prefix]bool, len(kept))
	for _, p := range kept {
		keptSet[p] = true
	}
	covered := map[netip.Prefix]bool{}
	for _, p := range prefixes {
		for _, anc := range ancestorPrefixes(p) {
			if keptSet[anc] {
				covered[p] = true
				break
			}
		}
	}
	return covered, nil
}

// announcedCoverersSQL takes "?" placeholders for the prefix ids, joined in at %s.
// The first placeholder is the user, then the mode.
const announcedCoverersSQL = `
SELECT DISTINCT cme.prefix_id
FROM catalog_mode_entries cme
JOIN services sv ON sv.id = cme.service_id
JOIN categories c ON c.id = sv.category_id
JOIN catalog_modes m ON m.id = cme.mode_id AND m.enabled = 1
JOIN selected_categories sc ON sc.user_id = ? AND sc.mode_id = cme.mode_id AND sc.category_id = c.id
WHERE cme.mode_id = ? AND cme.prefix_id IN (%s)`
