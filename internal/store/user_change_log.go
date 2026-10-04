package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// userChangeLogWindow is the longest span a user's change log reaches back.
const userChangeLogWindow = 30 * 24 * time.Hour

// EffectiveAuditRetentionDays is the retention the audit purge actually applies:
// a non-positive setting falls back to 30 days.
func EffectiveAuditRetentionDays(days int) int {
	if days <= 0 {
		return 30
	}
	return days
}

// UserChangeLogWindow is the span the change log may read: the 30-day cap, but
// never longer than the effective audit retention. Older audit rows are purged,
// and a feed sync can only be placed if every change after it is still recorded.
func UserChangeLogWindow(auditRetentionDays int) time.Duration {
	// Compared in days before converting: a huge setting would overflow
	// time.Duration if multiplied first.
	days := EffectiveAuditRetentionDays(auditRetentionDays)
	if days >= int(userChangeLogWindow/(24*time.Hour)) {
		return userChangeLogWindow
	}
	return time.Duration(days) * 24 * time.Hour
}

// userChangeLogListLimit caps each list (categories, services, routes) within
// one entry, so one large sync or bulk edit can't bloat the response. Anything
// past it is counted in Omitted rather than listed.
const userChangeLogListLimit = 100

// userChangeLogEntryLimit caps the entries returned for one user, newest first.
const userChangeLogEntryLimit = 200

// userChangeLogAuditLimit bounds the raw audit rows read for one user before
// merging with feed changes.
const userChangeLogAuditLimit = 500

// userChangeAuditActions are the audit actions recorded on a user's own row
// that the change log shows. Mode deletions reassign users through
// user.mode_changed, so they are covered too.
var userChangeAuditActions = []string{
	"user.mode_changed",
	"user.filter_mode_changed",
	"user.selections_changed",
	"route_filters.user_updated",
}

// userChangeAuditQuery reads one user's change-log audit rows. __ACTIONS__ is
// replaced with one placeholder per entry of userChangeAuditActions.
const userChangeAuditQuery = `
SELECT id, recorded_at, actor, action, COALESCE(before, ''), COALESCE(after, '')
FROM audit_log
WHERE object_type = 'user' AND object_id = ? AND recorded_at >= ? AND id > ?
	AND action IN (__ACTIONS__)
ORDER BY recorded_at DESC, id DESC
LIMIT ?`

// selectionAuditPayload is the before/after shape of a
// user.selections_changed audit row: the categories and services selected in
// one mode, sorted, so the change log can diff them by name.
type selectionAuditPayload struct {
	ModeID     int64        `json:"mode_id"`
	ModeName   string       `json:"mode_name"`
	Categories []string     `json:"categories"`
	Services   []ServiceKey `json:"services"`
}

// maxSelectionAuditNames bounds the names a selection audit row stores. A row
// that lists more is written as counts only, like a row from before names were
// stored: the change log can't reconstruct through it, and that is said, rather
// than writing a multi-megabyte payload for every edit to a large selection.
const maxSelectionAuditNames = 1000

// maxSelectionAuditBytes bounds the encoded size of a selection audit value, for
// the same reason: a single long name can be as costly as many short ones.
const maxSelectionAuditBytes = 64 << 10

// selectionAuditValue is the value to audit for a selection state: the names,
// or, once the selection is too large to store them, its counts plus a digest of
// the names. The digest keeps two different large selections with the same
// counts from comparing equal, which would drop the edit from the audit.
func selectionAuditValue(p selectionAuditPayload) any {
	names, err := json.Marshal(p)
	if err != nil {
		return p
	}
	if len(p.Categories)+len(p.Services) <= maxSelectionAuditNames && len(names) <= maxSelectionAuditBytes {
		return p
	}
	sum := sha256.Sum256(names)
	return boundedSelectionPayload{
		ModeID:     p.ModeID,
		ModeName:   p.ModeName,
		Oversized:  true,
		Categories: len(p.Categories),
		Services:   len(p.Services),
		Digest:     hex.EncodeToString(sum[:]),
	}
}

// boundedSelectionPayload is a selection too large to store by name. It keeps
// the mode, so the reader can treat it as unknown for that mode alone.
type boundedSelectionPayload struct {
	ModeID     int64  `json:"mode_id"`
	ModeName   string `json:"mode_name"`
	Oversized  bool   `json:"oversized"`
	Categories int    `json:"categories"`
	Services   int    `json:"services"`
	Digest     string `json:"digest"`
}

// boundedSelectionOf returns the bounded side of a selection change, whichever
// side was too large to store: a shrinking selection has one on the before side.
func boundedSelectionOf(before, after string) (boundedSelectionPayload, bool) {
	if b, ok := decodeBoundedSelection(after); ok {
		return b, true
	}
	return decodeBoundedSelection(before)
}

// decodeBoundedSelection reports whether a selection audit value is a bounded
// payload, and returns it if so.
func decodeBoundedSelection(raw string) (boundedSelectionPayload, bool) {
	var b boundedSelectionPayload
	if json.Unmarshal([]byte(raw), &b) != nil || !b.Oversized {
		return boundedSelectionPayload{}, false
	}
	return b, true
}

// selectionAuditState builds the audit payload for one mode's selection. Sorted
// output keeps the JSON stable, so an unchanged selection compares equal.
func selectionAuditState(modeID int64, modeName string, categories map[string]bool, services map[ServiceKey]bool) selectionAuditPayload {
	p := selectionAuditPayload{ModeID: modeID, ModeName: modeName, Categories: []string{}, Services: []ServiceKey{}}
	for c := range categories {
		p.Categories = append(p.Categories, c)
	}
	for k := range services {
		p.Services = append(p.Services, k)
	}
	sort.Strings(p.Categories)
	sortServiceKeys(p.Services)
	return p
}

// UserChangeList is one side (added or removed) of a change: the names it
// touched, each list capped, with Omitted counting what was not listed.
type UserChangeList struct {
	Categories []string     `json:"categories"`
	Services   []ServiceKey `json:"services"`
	Routes     []string     `json:"routes"`
	Omitted    int          `json:"omitted"`
}

// UserChangeEntry is one item in a user's change log.
//
// Source says who made the change: "self", "admin", or "feed_sync". Kind is
// "selections", "mode", "filter_mode", "route_filters", or "feed_sync". From
// and To carry the old and new value for mode and filter_mode changes; Mode
// names the mode a selection or feed change applies to.
type UserChangeEntry struct {
	// order is the commit position the entry was recorded at, on a doubled scale
	// so a sync can sit between the audit rows on either side of it: an audit row
	// is 2*ID, a sync that came after audit row S is 2*S+1. It orders entries that
	// share a second; it isn't part of the response.
	order int64
	// commit is the sync's change ID, which orders syncs that share a position.
	commit   int64
	At       int64          `json:"at"`
	Source   string         `json:"source"`
	Kind     string         `json:"kind"`
	Mode     string         `json:"mode,omitempty"`
	FeedName string         `json:"feed_name,omitempty"`
	From     string         `json:"from,omitempty"`
	To       string         `json:"to,omitempty"`
	Added    UserChangeList `json:"added"`
	Removed  UserChangeList `json:"removed"`
}

// UserChangeLog returns the changes to one user's selection, route filters,
// and mode over the last window, newest first. Admin and self-service changes
// come from the audit log; feed syncs are placed by the user's history at each
// sync (see userFeedSyncChanges). window comes from UserChangeLogWindow.
func (s *Store) UserChangeLog(ctx context.Context, userID int64, now time.Time, window time.Duration) ([]UserChangeEntry, error) {
	// One read transaction, so the current mode, the audit rows, and the feed
	// rows describe the same moment. Read separately, a selection save committing
	// between the reads could leave the reconstruction seeing a selection with no
	// audit row for it.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck
	var modeID, floorID int64
	if err := tx.QueryRowContext(ctx,
		"SELECT catalog_mode_id, history_from FROM users WHERE id = ?", userID).Scan(&modeID, &floorID); err != nil {
		return nil, err
	}
	since := now.Add(-window).Unix()
	entries, err := userAuditChanges(ctx, tx, userID, since, floorID)
	if err != nil {
		return nil, err
	}
	// Feed syncs are placed from the audit trail, which is only complete from the
	// boundary on. The direct entries above read whatever the audit still holds.
	complete, err := auditCompleteSince(ctx, tx, since)
	if err != nil {
		return nil, err
	}
	feedEntries, err := userFeedSyncChanges(ctx, tx, userID, modeID, complete, floorID)
	if err != nil {
		return nil, err
	}
	entries = append(entries, feedEntries...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].At != entries[j].At {
			return entries[i].At > entries[j].At
		}
		if entries[i].order != entries[j].order {
			return entries[i].order > entries[j].order
		}
		return entries[i].commit > entries[j].commit
	})
	if len(entries) > userChangeLogEntryLimit {
		entries = entries[:userChangeLogEntryLimit]
	}
	if entries == nil {
		entries = []UserChangeEntry{}
	}
	return entries, nil
}

// auditCompleteSince returns the later of from and the audit log's completeness
// boundary, so the window never reaches back over rows a purge already removed.
func auditCompleteSince(ctx context.Context, q queryer, from int64) (int64, error) {
	var complete int64
	err := q.QueryRowContext(ctx, "SELECT complete_since FROM audit_log_coverage WHERE id = 1").Scan(&complete)
	if errors.Is(err, sql.ErrNoRows) {
		return from, nil
	}
	if err != nil {
		return 0, err
	}
	return max(from, complete), nil
}

// syncOrder is a sync's commit position on the same scale as audit rows (see
// UserChangeEntry.order). A sync without a sequence has no position to offer.
func syncOrder(p syncPoint) int64 {
	if !p.hasSeq {
		return 0
	}
	return 2*p.seq + 1
}

// modeChangeAuditPayload is the before/after shape of a user.mode_changed row.
// The name is captured when the change is written, so the log keeps the name
// the mode had then, even after a rename or after the mode is deleted and its
// ID reused.
type modeChangeAuditPayload struct {
	CatalogModeID   int64  `json:"catalog_mode_id"`
	CatalogModeName string `json:"catalog_mode_name"`
}

// modeChangeState reads a mode's audit payload inside tx.
func modeChangeState(ctx context.Context, tx *sql.Tx, modeID int64) (modeChangeAuditPayload, error) {
	name, err := modeNameTx(ctx, tx, modeID)
	if err != nil {
		return modeChangeAuditPayload{}, err
	}
	return modeChangeAuditPayload{CatalogModeID: modeID, CatalogModeName: name}, nil
}

// modeNameTx returns a catalog mode's name, or "" when no such mode exists.
func modeNameTx(ctx context.Context, tx *sql.Tx, modeID int64) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, "SELECT name FROM catalog_modes WHERE id = ?", modeID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// modeNameOr is a snapshotted mode name, or the bare ID for a snapshot written
// without one (a row that predates names, or a mode that had no name).
func modeNameOr(name string, id int64) string {
	if name != "" {
		return name
	}
	return "#" + strconv.FormatInt(id, 10)
}

// changeSource maps an audit actor to who made the change. Admin actors are
// not identified further: the log is the user's view, and it must not expose
// the operator's address.
func changeSource(actor string) string {
	if strings.HasPrefix(actor, "user:") {
		return "self"
	}
	return "admin"
}

func userAuditChanges(ctx context.Context, q queryer, userID, since, floorID int64) ([]UserChangeEntry, error) {
	args := []any{strconv.FormatInt(userID, 10), since, floorID}
	placeholders := make([]string, 0, len(userChangeAuditActions))
	for _, action := range userChangeAuditActions {
		placeholders = append(placeholders, "?")
		args = append(args, action)
	}
	args = append(args, userChangeLogAuditLimit)
	// Only "?" placeholders are substituted; every value is still bound.
	query := strings.Replace(userChangeAuditQuery, "__ACTIONS__", strings.Join(placeholders, ", "), 1)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []UserChangeEntry
	for rows.Next() {
		var id, at int64
		var actor, action, before, after string
		if err := rows.Scan(&id, &at, &actor, &action, &before, &after); err != nil {
			return nil, err
		}
		e := UserChangeEntry{order: 2 * id, At: at, Source: changeSource(actor)}
		switch action {
		case "user.selections_changed":
			e.Kind = "selections"
			fillSelectionChange(&e, before, after)
		case "user.mode_changed":
			e.Kind = "mode"
			fillModeChange(&e, before, after)
		case "user.filter_mode_changed":
			e.Kind = "filter_mode"
			fillFilterModeChange(&e, before, after)
		case "route_filters.user_updated":
			e.Kind = "route_filters"
			fillRouteChange(&e, before, after)
		}
		capChangeList(&e.Added)
		capChangeList(&e.Removed)
		out = append(out, e)
	}
	return out, rows.Err()
}

// fillSelectionChange diffs two selection payloads by name. Rows written
// before the payload carried names hold only counts, which don't decode into
// the name lists; those entries keep their kind and source but list nothing.
func fillSelectionChange(e *UserChangeEntry, before, after string) {
	var b, a selectionAuditPayload
	if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
		// Too large to list: name the mode it was in and leave the lists empty.
		if bounded, ok := boundedSelectionOf(before, after); ok {
			e.Mode = modeNameOr(bounded.ModeName, bounded.ModeID)
		}
		return
	}
	e.Mode = modeNameOr(a.ModeName, a.ModeID)
	e.Added.Categories, e.Removed.Categories = diffSets(b.Categories, a.Categories)
	e.Added.Services, e.Removed.Services = diffSets(b.Services, a.Services)
}

func fillModeChange(e *UserChangeEntry, before, after string) {
	var b, a modeChangeAuditPayload
	if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
		return
	}
	e.From = modeNameOr(b.CatalogModeName, b.CatalogModeID)
	e.To = modeNameOr(a.CatalogModeName, a.CatalogModeID)
}

func fillFilterModeChange(e *UserChangeEntry, before, after string) {
	var b, a map[string]string
	if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
		return
	}
	e.From = b["filter_mode"]
	e.To = a["filter_mode"]
}

// fillRouteChange reads the audit payload written by setUserRouteFiltersTx,
// where before holds the removed filters and after the added ones.
func fillRouteChange(e *UserChangeEntry, before, after string) {
	var removed, added AuditRouteFilters
	if json.Unmarshal([]byte(before), &removed) != nil || json.Unmarshal([]byte(after), &added) != nil {
		return
	}
	e.Removed.Routes, e.Removed.Omitted = routeLabels(removed)
	e.Added.Routes, e.Added.Omitted = routeLabels(added)
}

func routeLabels(f AuditRouteFilters) ([]string, int) {
	out := []string{}
	for _, v := range f.Allow.Entries {
		out = append(out, "allow "+v)
	}
	for _, v := range f.Deny.Entries {
		out = append(out, "deny "+v)
	}
	return out, f.Allow.Truncated + f.Deny.Truncated
}

// selectionChange is one audited selection change, oldest first. legacy marks
// a row written before payloads carried names: it could have changed any mode,
// so the selection before it can't be recovered.
type selectionChange struct {
	id     int64
	at     int64
	legacy bool
	modeID int64
	before selectionAuditPayload
}

// modeChange is one audited catalog mode move. known is false if its payload
// didn't decode, in which case the mode before it is unknown.
type modeChange struct {
	id     int64
	at     int64
	known  bool
	before int64
}

// userHistory rebuilds what a user had at an earlier time. Every change is
// audited, so the state at time T is the "before" of the first change after T,
// and the current state when no change follows T.
type userHistory struct {
	q           queryer
	userID      int64
	currentMode int64
	selections  []selectionChange
	modes       []modeChange
	selCache    map[selectionKey]selectionValue
	// live is the current selection per mode, read once: every sync that no
	// later change touches falls back to it.
	live map[int64]selectionValue
}

// syncPoint places a sync against the audit trail. Where audit_seq is known,
// the sync happened right after the audit row with that ID, which orders it
// exactly, including within a second. Older syncs only have their time.
type syncPoint struct {
	at     int64
	seq    int64
	hasSeq bool
}

type selectionKey struct {
	point  syncPoint
	modeID int64
}

// startIndex is the first change that happened after the sync. Changes in the
// sync's own second are ordered by ID when the sync has a sequence; without one,
// a change in that second can't be ordered, and the caller has to treat it as
// unknown (see modeAt and selectionAt).
func startIndex(n int, at func(i int) (int64, int64), p syncPoint) int {
	if p.hasSeq {
		return sort.Search(n, func(i int) bool {
			t, id := at(i)
			return t > p.at || (t == p.at && id > p.seq)
		})
	}
	return sort.Search(n, func(i int) bool {
		t, _ := at(i)
		return t >= p.at
	})
}

type selectionValue struct {
	categories map[string]bool
	services   map[ServiceKey]bool
	known      bool
}

func newUserHistory(ctx context.Context, q queryer, userID, currentMode, since, floorID int64) (*userHistory, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id, recorded_at, action, COALESCE(before, ''), COALESCE(after, '')
FROM audit_log
WHERE object_type = 'user' AND object_id = ? AND recorded_at >= ? AND id > ?
	AND action IN ('user.selections_changed', 'user.mode_changed')
ORDER BY recorded_at ASC, id ASC`, strconv.FormatInt(userID, 10), since, floorID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	h := &userHistory{q: q, userID: userID, currentMode: currentMode,
		selCache: map[selectionKey]selectionValue{}, live: map[int64]selectionValue{}}
	for rows.Next() {
		var id, at int64
		var action, before, after string
		if err := rows.Scan(&id, &at, &action, &before, &after); err != nil {
			return nil, err
		}
		if action == "user.mode_changed" {
			var b modeChangeAuditPayload
			known := json.Unmarshal([]byte(before), &b) == nil
			h.modes = append(h.modes, modeChange{id: id, at: at, known: known, before: b.CatalogModeID})
			continue
		}
		var b, a selectionAuditPayload
		beforeErr := json.Unmarshal([]byte(before), &b)
		if beforeErr == nil && json.Unmarshal([]byte(after), &a) != nil {
			// The selection grew past the bound: the state before this change is
			// still exact, so earlier syncs can be placed against it.
			if bounded, ok := decodeBoundedSelection(after); ok {
				h.selections = append(h.selections, selectionChange{id: id, at: at, modeID: bounded.ModeID, before: b})
				continue
			}
		}
		if beforeErr != nil || json.Unmarshal([]byte(after), &a) != nil {
			// Unknown: the row is bounded or predates names. A bounded row is unknown
			// for its own mode only; any other row is unknown for every mode.
			bounded, _ := boundedSelectionOf(before, after)
			h.selections = append(h.selections, selectionChange{id: id, at: at, legacy: true, modeID: bounded.ModeID})
			continue
		}
		h.selections = append(h.selections, selectionChange{id: id, at: at, modeID: a.ModeID, before: b})
	}
	return h, rows.Err()
}

// modeAt returns the catalog mode the user was in at time at. A change in the
// same second as at is ambiguous at this resolution, so it reports unknown.
func (h *userHistory) modeAt(p syncPoint) (modeID int64, known bool) {
	i := startIndex(len(h.modes), func(i int) (int64, int64) { return h.modes[i].at, h.modes[i].id }, p)
	if i == len(h.modes) {
		return h.currentMode, true
	}
	if !p.hasSeq && h.modes[i].at == p.at {
		return 0, false
	}
	return h.modes[i].before, h.modes[i].known
}

// selectionAt returns the user's selection in modeID at time at. known is false
// when the first change that could touch modeID is a legacy row, or lands in the
// same second as at, where its order against the sync can't be told apart.
func (h *userHistory) selectionAt(ctx context.Context, p syncPoint, modeID int64) (selectionValue, error) {
	key := selectionKey{point: p, modeID: modeID}
	if v, ok := h.selCache[key]; ok {
		return v, nil
	}
	var v selectionValue
	v.known = true
	found := false
	start := startIndex(len(h.selections), func(i int) (int64, int64) { return h.selections[i].at, h.selections[i].id }, p)
	for i := start; i < len(h.selections); i++ {
		c := h.selections[i]
		if c.legacy && (c.modeID == 0 || c.modeID == modeID) {
			v.known = false
			found = true
			break
		}
		if c.modeID != modeID {
			continue
		}
		if !p.hasSeq && c.at == p.at {
			v.known = false
			found = true
			break
		}
		v.categories = make(map[string]bool, len(c.before.Categories))
		for _, cat := range c.before.Categories {
			v.categories[cat] = true
		}
		v.services = make(map[ServiceKey]bool, len(c.before.Services))
		for _, svc := range c.before.Services {
			v.services[svc] = true
		}
		found = true
		break
	}
	if !found {
		live, ok := h.live[modeID]
		if !ok {
			cats, svcs, err := userModeSelection(ctx, h.q, h.userID, modeID)
			if err != nil {
				return selectionValue{}, err
			}
			live = selectionValue{categories: cats, services: svcs, known: true}
			h.live[modeID] = live
		}
		v.categories, v.services = live.categories, live.services
	}
	h.selCache[key] = v
	return v, nil
}

// reach returns every mode the user was in during the window and every category
// they selected in one of those modes, whether currently or at some recorded
// point. Each state the history can answer with is a recorded "before" or the
// current selection, so nothing outside these sets can be relevant.
func (h *userHistory) reach(ctx context.Context) (modes []int64, categories []string, err error) {
	modeSet := map[int64]bool{h.currentMode: true}
	for _, m := range h.modes {
		if m.known {
			modeSet[m.before] = true
		}
	}
	catSet := map[string]bool{}
	for _, c := range h.selections {
		if c.legacy {
			continue
		}
		for _, cat := range c.before.Categories {
			catSet[cat] = true
		}
		for _, svc := range c.before.Services {
			catSet[svc.Category] = true
		}
	}
	for m := range modeSet {
		modes = append(modes, m)
		cats, svcs, err := userModeSelection(ctx, h.q, h.userID, m)
		if err != nil {
			return nil, nil, err
		}
		for cat := range cats {
			catSet[cat] = true
		}
		for svc := range svcs {
			catSet[svc.Category] = true
		}
	}
	for cat := range catSet {
		categories = append(categories, cat)
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i] < modes[j] })
	sort.Strings(categories)
	return modes, categories, nil
}

// userFeedSyncChanges lists the services feed syncs added or removed that the
// user had selected at the time of each sync, in the mode the user was in then.
// Selection and mode at each sync come from the audit trail, so changing a
// selection later doesn't rewrite what an earlier sync meant for the user.
// A sync that falls before a change the trail can't reconstruct is left out.
func userFeedSyncChanges(ctx context.Context, q queryer, userID, currentMode, since, floorID int64) ([]UserChangeEntry, error) {
	history, err := newUserHistory(ctx, q, userID, currentMode, since, floorID)
	if err != nil {
		return nil, err
	}
	modes, categories, err := history.reach(ctx)
	if err != nil {
		return nil, err
	}
	if len(modes) == 0 || len(categories) == 0 {
		return nil, nil
	}
	// The scan counts services per (sync, mode, kind, category) first. Only the
	// categories and services the user actually selected at each sync are then
	// read by name, capped, so a huge category isn't expanded row by row. A
	// feed's enabled state and mode assignments today say nothing about the
	// syncs already recorded, so neither filters here.
	type group struct {
		modeID, changeID, syncedAt, seq int64
		hasSeq                          bool
		modeName, feedName, kind, cat   string
		count                           int
	}
	var groups []group
	for start := 0; start < len(categories); start += maxScanCategories {
		chunk := categories[start:min(start+maxScanCategories, len(categories))]
		args := []any{since}
		for _, m := range modes {
			args = append(args, m)
		}
		for _, c := range chunk {
			args = append(args, c)
		}
		rows, err := q.QueryContext(ctx, strings.NewReplacer(
			"__MODES__", placeholders(len(modes)),
			"__CATEGORIES__", placeholders(len(chunk)),
		).Replace(feedSyncGroupQuery), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var g group
			var seq sql.NullInt64
			if err := rows.Scan(&g.modeID, &g.modeName, &g.changeID, &g.feedName, &g.syncedAt, &seq, &g.kind, &g.cat, &g.count); err != nil {
				_ = rows.Close() //nolint:errcheck
				return nil, err
			}
			g.seq, g.hasSeq = seq.Int64, seq.Valid
			groups = append(groups, g)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close() //nolint:errcheck
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}

	type entryKey struct{ changeID, modeID int64 }
	var out []UserChangeEntry
	index := map[entryKey]int{}
	for _, g := range groups {
		point := syncPoint{at: g.syncedAt, seq: g.seq, hasSeq: g.hasSeq}
		// Only syncs after the user's history floor are this user's. A sync with no
		// sequence predates the audit trail, so it can't be placed at all.
		if g.seq < floorID || !g.hasSeq {
			continue
		}
		mode, known := history.modeAt(point)
		if !known || mode != g.modeID {
			continue
		}
		sel, err := history.selectionAt(ctx, point, g.modeID)
		if err != nil {
			return nil, err
		}
		if !sel.known {
			continue
		}
		var names []string
		var count int
		if sel.categories[g.cat] {
			// The whole category is selected: every service of it counts.
			names, err = serviceNamesTx(ctx, q, g.changeID, g.kind, g.cat, maxListedNames+1)
			if err != nil {
				return nil, err
			}
			count = g.count
		} else {
			selected := map[string]bool{}
			for svc := range sel.services {
				if svc.Category == g.cat {
					selected[svc.Service] = true
				}
			}
			if len(selected) == 0 {
				continue
			}
			names, count, err = matchedServiceNamesTx(ctx, q, g.changeID, g.kind, g.cat, selected, maxListedNames)
			if err != nil {
				return nil, err
			}
		}
		if count == 0 {
			continue
		}
		listed := min(len(names), maxListedNames)
		ek := entryKey{changeID: g.changeID, modeID: g.modeID}
		i, ok := index[ek]
		if !ok {
			out = append(out, UserChangeEntry{
				order:    syncOrder(point),
				commit:   g.changeID,
				At:       g.syncedAt,
				Source:   "feed_sync",
				Kind:     "feed_sync",
				Mode:     modeNameOr(g.modeName, g.modeID),
				FeedName: g.feedName,
			})
			i = len(out) - 1
			index[ek] = i
		}
		side := &out[i].Removed
		if g.kind == "added" {
			side = &out[i].Added
		}
		for _, n := range names[:listed] {
			side.Services = append(side.Services, ServiceKey{Category: g.cat, Service: n})
		}
		side.Omitted += count - listed
	}
	for i := range out {
		sortServiceKeys(out[i].Added.Services)
		sortServiceKeys(out[i].Removed.Services)
		capChangeList(&out[i].Added)
		capChangeList(&out[i].Removed)
	}
	return out, nil
}

// maxScanCategories bounds the categories in one scan statement, well under
// SQLite's bound-variable limit.
const maxScanCategories = 500

// feedSyncGroupQuery counts a sync's services per mode, kind, and category.
// __MODES__ and __CATEGORIES__ are replaced with one placeholder per value.
const feedSyncGroupQuery = `
SELECT cm.mode_id, cm.mode_name, c.id, c.feed_name, c.synced_at, c.audit_seq, s.kind, s.category, COUNT(*)
FROM feed_sync_change_services s
JOIN feed_sync_changes c ON c.id = s.change_id
JOIN feed_sync_change_modes cm ON cm.change_id = c.id
WHERE c.synced_at >= ? AND cm.mode_id IN (__MODES__) AND s.category IN (__CATEGORIES__)
GROUP BY cm.mode_id, cm.mode_name, c.id, c.feed_name, c.synced_at, c.audit_seq, s.kind, s.category
ORDER BY c.synced_at DESC, c.id DESC, cm.mode_id, s.kind, s.category`

// maxListedNames bounds the service names one side of an entry lists.
const maxListedNames = userChangeLogListLimit

// serviceNamesTx reads up to limit service names one sync added or removed in
// one category, in name order. A whole selected category is listed this way.
func serviceNamesTx(ctx context.Context, q queryer, changeID int64, kind, category string, limit int) ([]string, error) {
	rows, err := q.QueryContext(ctx, feedSyncNamesQuery+" LIMIT ?", changeID, kind, category, limit)
	if err != nil {
		return nil, err
	}
	return scanNames(rows)
}

// matchedServiceNamesTx streams one sync's services in a category and keeps the
// first limit names the user selected individually, plus the exact number that
// matched. Nothing else is materialized, however large the category is.
func matchedServiceNamesTx(ctx context.Context, q queryer, changeID int64, kind, category string, selected map[string]bool, limit int) ([]string, int, error) {
	rows, err := q.QueryContext(ctx, feedSyncNamesQuery, changeID, kind, category)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var names []string
	count := 0
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, 0, err
		}
		if !selected[n] {
			continue
		}
		count++
		if len(names) < limit {
			names = append(names, n)
		}
	}
	return names, count, rows.Err()
}

func scanNames(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

const feedSyncNamesQuery = `
SELECT service FROM feed_sync_change_services
WHERE change_id = ? AND kind = ? AND category = ?
ORDER BY service`

// placeholders returns n comma-separated "?" placeholders.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// diffSets returns the elements only in after (added) and only in before
// (removed), in the order they appear in their source slice.
func diffSets[T comparable](before, after []T) (added, removed []T) {
	inBefore := make(map[T]bool, len(before))
	for _, v := range before {
		inBefore[v] = true
	}
	inAfter := make(map[T]bool, len(after))
	for _, v := range after {
		inAfter[v] = true
	}
	added = []T{}
	for _, v := range after {
		if !inBefore[v] {
			added = append(added, v)
		}
	}
	removed = []T{}
	for _, v := range before {
		if !inAfter[v] {
			removed = append(removed, v)
		}
	}
	return added, removed
}

// capChangeList trims each list to userChangeLogListLimit, counting the
// dropped items in Omitted. Lists are never nil so they encode as [].
func capChangeList(l *UserChangeList) {
	var omitted int
	l.Categories, omitted = capList(l.Categories)
	l.Omitted += omitted
	l.Services, omitted = capList(l.Services)
	l.Omitted += omitted
	l.Routes, omitted = capList(l.Routes)
	l.Omitted += omitted
}

func capList[T any](in []T) ([]T, int) {
	if in == nil {
		return []T{}, 0
	}
	if len(in) <= userChangeLogListLimit {
		return in, 0
	}
	return in[:userChangeLogListLimit], len(in) - userChangeLogListLimit
}
