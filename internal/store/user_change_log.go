package store

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// userChangeLogWindow bounds how far back a user's change log reaches. It
// matches the default audit retention, so the two sources age out together.
const userChangeLogWindow = 30 * 24 * time.Hour

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
	Categories []string     `json:"categories"`
	Services   []ServiceKey `json:"services"`
}

// selectionAuditState builds the audit payload for one mode's selection. Sorted
// output keeps the JSON stable, so an unchanged selection compares equal.
func selectionAuditState(modeID int64, categories map[string]bool, services map[ServiceKey]bool) selectionAuditPayload {
	p := selectionAuditPayload{ModeID: modeID, Categories: []string{}, Services: []ServiceKey{}}
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
// and mode over the last userChangeLogWindow, newest first. Admin and
// self-service changes come from the audit log; feed syncs come from
// feed_sync_change_services, limited to services the user has selected in
// their current mode, so the log only shows changes that touch the user.
func (s *Store) UserChangeLog(ctx context.Context, userID int64, now time.Time) ([]UserChangeEntry, error) {
	var modeID int64
	if err := s.DB.QueryRowContext(ctx,
		"SELECT catalog_mode_id FROM users WHERE id = ?", userID).Scan(&modeID); err != nil {
		return nil, err
	}
	modeNames, err := s.catalogModeNames(ctx)
	if err != nil {
		return nil, err
	}
	since := now.Add(-userChangeLogWindow).Unix()
	entries, err := s.userAuditChanges(ctx, userID, since, modeNames)
	if err != nil {
		return nil, err
	}
	feedEntries, err := s.userFeedSyncChanges(ctx, userID, modeID, since, modeNames)
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

func (s *Store) catalogModeNames(ctx context.Context) (map[int64]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, name FROM catalog_modes")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}

// modeLabel names a mode, falling back to its ID once the mode is deleted.
func modeLabel(names map[int64]string, id int64) string {
	if name, ok := names[id]; ok {
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

func (s *Store) userAuditChanges(ctx context.Context, userID, since int64, modeNames map[int64]string) ([]UserChangeEntry, error) {
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
			fillSelectionChange(&e, before, after, modeNames)
		case "user.mode_changed":
			e.Kind = "mode"
			fillModeChange(&e, before, after, modeNames)
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
func fillSelectionChange(e *UserChangeEntry, before, after string, modeNames map[int64]string) {
	var b, a selectionAuditPayload
	if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
		return
	}
	e.Mode = modeLabel(modeNames, a.ModeID)
	e.Added.Categories, e.Removed.Categories = diffSets(b.Categories, a.Categories)
	e.Added.Services, e.Removed.Services = diffSets(b.Services, a.Services)
}

func fillModeChange(e *UserChangeEntry, before, after string, modeNames map[int64]string) {
	var b, a map[string]int64
	if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
		return
	}
	e.From = modeLabel(modeNames, b["catalog_mode_id"])
	e.To = modeLabel(modeNames, a["catalog_mode_id"])
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
			var b map[string]int64
			known := json.Unmarshal([]byte(before), &b) == nil
			h.modes = append(h.modes, modeChange{at: at, known: known, before: b["catalog_mode_id"]})
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

// modeAt returns the catalog mode the user was in at time at.
func (h *userHistory) modeAt(at int64) (modeID int64, known bool) {
	i := sort.Search(len(h.modes), func(i int) bool { return h.modes[i].at > at })
	if i == len(h.modes) {
		return h.currentMode, true
	}
	return h.modes[i].before, h.modes[i].known
}

// selectionAt returns the user's selection in modeID at time at. known is false
// when the first change after at that could touch modeID is a legacy row.
func (h *userHistory) selectionAt(ctx context.Context, at, modeID int64) (selectionValue, error) {
	key := selectionKey{at: at, modeID: modeID}
	if v, ok := h.selCache[key]; ok {
		return v, nil
	}
	var v selectionValue
	v.known = true
	found := false
	for i := sort.Search(len(h.selections), func(i int) bool { return h.selections[i].at > at }); i < len(h.selections); i++ {
		c := h.selections[i]
		if c.legacy {
			v.known = false
			found = true
			break
		}
		if c.modeID != modeID {
			continue
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

// userFeedSyncChanges lists the services feed syncs added or removed that the
// user had selected at the time of each sync, in the mode the user was in then.
// Selection and mode at each sync come from the audit trail, so changing a
// selection later doesn't rewrite what an earlier sync meant for the user.
// A sync that falls before a change the trail can't reconstruct is left out.
func (s *Store) userFeedSyncChanges(ctx context.Context, userID, currentMode, since int64, modeNames map[int64]string) ([]UserChangeEntry, error) {
	history, err := s.newUserHistory(ctx, userID, currentMode, since)
	if err != nil {
		return nil, err
	}
	includeModes, err := s.includeModesByFeed(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT c.feed_id, f.name, c.id, c.synced_at, s.kind, s.category, s.service
FROM feed_sync_change_services s
JOIN feed_sync_changes c ON c.id = s.change_id
JOIN feeds f ON f.id = c.feed_id AND f.enabled = 1
WHERE c.synced_at >= ?
ORDER BY c.synced_at DESC, c.id DESC, s.kind, s.category, s.service`, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	type entryKey struct{ changeID, modeID int64 }
	var out []UserChangeEntry
	index := map[entryKey]int{}
	for rows.Next() {
		var feedID, changeID, syncedAt int64
		var feedName, kind, category, service string
		if err := rows.Scan(&feedID, &feedName, &changeID, &syncedAt, &kind, &category, &service); err != nil {
			return nil, err
		}
		key := ServiceKey{Category: category, Service: service}
		for _, modeID := range includeModes[feedID] {
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
					Mode:     modeLabel(modeNames, modeID),
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

// includeModesByFeed maps each feed to the enabled modes that include it.
func (s *Store) includeModesByFeed(ctx context.Context) (map[int64][]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT cmf.feed_id, cmf.mode_id
FROM catalog_mode_feeds cmf
JOIN catalog_modes m ON m.id = cmf.mode_id AND m.enabled = 1
WHERE cmf.exclude = 0`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	out := map[int64][]int64{}
	for rows.Next() {
		var feedID, modeID int64
		if err := rows.Scan(&feedID, &modeID); err != nil {
			return nil, err
		}
		out[feedID] = append(out[feedID], modeID)
	}
	return out, rows.Err()
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
