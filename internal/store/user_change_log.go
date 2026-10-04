package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// userChangeLogWindow is the longest span a user's change log reaches back.
const userChangeLogWindow = 30 * 24 * time.Hour

// UserChangeLogWindow is the span the change log may read: the 30-day cap, but
// never longer than audit retention. Older audit rows are purged, and a feed
// sync can only be placed if every change after it is still recorded. A
// non-positive retention purges the whole audit log on every run, so the window
// is empty.
func UserChangeLogWindow(auditRetentionDays int) time.Duration {
	if auditRetentionDays <= 0 {
		return 0
	}
	window := userChangeLogWindow
	if retention := time.Duration(auditRetentionDays) * 24 * time.Hour; retention < window {
		window = retention
	}
	return window
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
SELECT recorded_at, actor, action, COALESCE(before, ''), COALESCE(after, '')
FROM audit_log
WHERE object_type = 'user' AND object_id = ? AND recorded_at >= ?
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
	var modeID int64
	if err := s.DB.QueryRowContext(ctx,
		"SELECT catalog_mode_id FROM users WHERE id = ?", userID).Scan(&modeID); err != nil {
		return nil, err
	}
	since, err := s.auditCompleteSince(ctx, now.Add(-window).Unix())
	if err != nil {
		return nil, err
	}
	entries, err := s.userAuditChanges(ctx, userID, since)
	if err != nil {
		return nil, err
	}
	feedEntries, err := s.userFeedSyncChanges(ctx, userID, modeID, since)
	if err != nil {
		return nil, err
	}
	entries = append(entries, feedEntries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].At > entries[j].At })
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
func (s *Store) auditCompleteSince(ctx context.Context, from int64) (int64, error) {
	var complete int64
	err := s.DB.QueryRowContext(ctx, "SELECT complete_since FROM audit_log_coverage WHERE id = 1").Scan(&complete)
	if errors.Is(err, sql.ErrNoRows) {
		return from, nil
	}
	if err != nil {
		return 0, err
	}
	return max(from, complete), nil
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

func (s *Store) userAuditChanges(ctx context.Context, userID, since int64) ([]UserChangeEntry, error) {
	args := []any{strconv.FormatInt(userID, 10), since}
	placeholders := make([]string, 0, len(userChangeAuditActions))
	for _, action := range userChangeAuditActions {
		placeholders = append(placeholders, "?")
		args = append(args, action)
	}
	args = append(args, userChangeLogAuditLimit)
	// Only "?" placeholders are substituted; every value is still bound.
	query := strings.Replace(userChangeAuditQuery, "__ACTIONS__", strings.Join(placeholders, ", "), 1)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []UserChangeEntry
	for rows.Next() {
		var at int64
		var actor, action, before, after string
		if err := rows.Scan(&at, &actor, &action, &before, &after); err != nil {
			return nil, err
		}
		e := UserChangeEntry{At: at, Source: changeSource(actor)}
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
	at     int64
	legacy bool
	modeID int64
	before selectionAuditPayload
}

// modeChange is one audited catalog mode move. known is false if its payload
// didn't decode, in which case the mode before it is unknown.
type modeChange struct {
	at     int64
	known  bool
	before int64
}

// userHistory rebuilds what a user had at an earlier time. Every change is
// audited, so the state at time T is the "before" of the first change after T,
// and the current state when no change follows T.
type userHistory struct {
	store       *Store
	userID      int64
	currentMode int64
	selections  []selectionChange
	modes       []modeChange
	selCache    map[selectionKey]selectionValue
}

type selectionKey struct{ at, modeID int64 }

type selectionValue struct {
	categories map[string]bool
	services   map[ServiceKey]bool
	known      bool
}

func (s *Store) newUserHistory(ctx context.Context, userID, currentMode, since int64) (*userHistory, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT recorded_at, action, COALESCE(before, ''), COALESCE(after, '')
FROM audit_log
WHERE object_type = 'user' AND object_id = ? AND recorded_at >= ?
	AND action IN ('user.selections_changed', 'user.mode_changed')
ORDER BY recorded_at ASC, id ASC`, strconv.FormatInt(userID, 10), since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	h := &userHistory{store: s, userID: userID, currentMode: currentMode, selCache: map[selectionKey]selectionValue{}}
	for rows.Next() {
		var at int64
		var action, before, after string
		if err := rows.Scan(&at, &action, &before, &after); err != nil {
			return nil, err
		}
		if action == "user.mode_changed" {
			var b modeChangeAuditPayload
			known := json.Unmarshal([]byte(before), &b) == nil
			h.modes = append(h.modes, modeChange{at: at, known: known, before: b.CatalogModeID})
			continue
		}
		var b, a selectionAuditPayload
		if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
			h.selections = append(h.selections, selectionChange{at: at, legacy: true})
			continue
		}
		h.selections = append(h.selections, selectionChange{at: at, modeID: a.ModeID, before: b})
	}
	return h, rows.Err()
}

// modeAt returns the catalog mode the user was in at time at. A change in the
// same second as at is ambiguous at this resolution, so it reports unknown.
func (h *userHistory) modeAt(at int64) (modeID int64, known bool) {
	i := sort.Search(len(h.modes), func(i int) bool { return h.modes[i].at >= at })
	if i == len(h.modes) {
		return h.currentMode, true
	}
	if h.modes[i].at == at {
		return 0, false
	}
	return h.modes[i].before, h.modes[i].known
}

// selectionAt returns the user's selection in modeID at time at. known is false
// when the first change that could touch modeID is a legacy row, or lands in the
// same second as at, where its order against the sync can't be told apart.
func (h *userHistory) selectionAt(ctx context.Context, at, modeID int64) (selectionValue, error) {
	key := selectionKey{at: at, modeID: modeID}
	if v, ok := h.selCache[key]; ok {
		return v, nil
	}
	var v selectionValue
	v.known = true
	found := false
	for i := sort.Search(len(h.selections), func(i int) bool { return h.selections[i].at >= at }); i < len(h.selections); i++ {
		c := h.selections[i]
		if c.legacy {
			v.known = false
			found = true
			break
		}
		if c.modeID != modeID {
			continue
		}
		if c.at == at {
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
		cats, svcs, err := userModeSelection(ctx, h.store.DB, h.userID, modeID)
		if err != nil {
			return selectionValue{}, err
		}
		v.categories, v.services = cats, svcs
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
		cats, svcs, err := userModeSelection(ctx, h.store.DB, h.userID, m)
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
func (s *Store) userFeedSyncChanges(ctx context.Context, userID, currentMode, since int64) ([]UserChangeEntry, error) {
	history, err := s.newUserHistory(ctx, userID, currentMode, since)
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
	// Only a service in a category the user selected at some point in the
	// window can ever count (selecting a service also names its category), so
	// the scan is narrowed to those modes and categories before expanding rows.
	// A feed's enabled state and mode assignments today say nothing about the
	// syncs already recorded, so neither filters here: each sync carries the
	// modes it reached, and history outlives a feed being disabled.
	args := []any{since}
	for _, m := range modes {
		args = append(args, m)
	}
	for _, c := range categories {
		args = append(args, c)
	}
	rows, err := s.DB.QueryContext(ctx, strings.NewReplacer(
		"__MODES__", placeholders(len(modes)),
		"__CATEGORIES__", placeholders(len(categories)),
	).Replace(feedSyncScanQuery), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	type entryKey struct{ changeID, modeID int64 }
	var out []UserChangeEntry
	index := map[entryKey]int{}
	for rows.Next() {
		var modeID, changeID, syncedAt int64
		var modeName, feedName, kind, category, service string
		if err := rows.Scan(&modeID, &modeName, &changeID, &feedName, &syncedAt, &kind, &category, &service); err != nil {
			return nil, err
		}
		key := ServiceKey{Category: category, Service: service}
		mode, known := history.modeAt(syncedAt)
		if !known || mode != modeID {
			continue
		}
		sel, err := history.selectionAt(ctx, syncedAt, modeID)
		if err != nil {
			return nil, err
		}
		if !sel.known || (!sel.categories[category] && !sel.services[key]) {
			continue
		}
		ek := entryKey{changeID: changeID, modeID: modeID}
		i, ok := index[ek]
		if !ok {
			out = append(out, UserChangeEntry{
				At:       syncedAt,
				Source:   "feed_sync",
				Kind:     "feed_sync",
				Mode:     modeNameOr(modeName, modeID),
				FeedName: feedName,
			})
			i = len(out) - 1
			index[ek] = i
		}
		if kind == "added" {
			out[i].Added.Services = append(out[i].Added.Services, key)
		} else {
			out[i].Removed.Services = append(out[i].Removed.Services, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		capChangeList(&out[i].Added)
		capChangeList(&out[i].Removed)
	}
	return out, nil
}

// feedSyncScanQuery reads the service rows of recorded syncs for the given modes
// and categories, oldest first within a sync. __MODES__ and __CATEGORIES__ are
// replaced with one placeholder per value.
const feedSyncScanQuery = `
SELECT cm.mode_id, cm.mode_name, c.id, c.feed_name, c.synced_at, s.kind, s.category, s.service
FROM feed_sync_change_services s
JOIN feed_sync_changes c ON c.id = s.change_id
JOIN feed_sync_change_modes cm ON cm.change_id = c.id
WHERE c.synced_at >= ? AND cm.mode_id IN (__MODES__) AND s.category IN (__CATEGORIES__)
ORDER BY c.synced_at DESC, c.id DESC, cm.mode_id, s.kind, s.category, s.service`

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
