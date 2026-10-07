package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
)

// ConfigSnapshotSchemaVersion is bumped whenever ConfigSnapshot's shape
// changes in a way that could break a consumer reading an older export.
const ConfigSnapshotSchemaVersion = 1

// ConfigModeFeedLink is one feed a mode includes or excludes, by the feed's
// name rather than its ID: IDs aren't stable across instances, and this is
// exactly what import needs to resolve on whichever instance applies it.
type ConfigModeFeedLink struct {
	Feed    string `json:"feed"`
	Exclude bool   `json:"exclude"`
}

// ConfigCommunity is one category's or service's community number. Service
// empty means the category's own (group-level) number.
type ConfigCommunity struct {
	Category  string `json:"category"`
	Service   string `json:"service,omitempty"`
	Community uint32 `json:"community"`
}

// ConfigMode is one catalog mode's configuration: its feed membership and
// community numbering. Feeds themselves aren't part of the snapshot — they
// have their own export, and a custom adapter's Data can carry credentials —
// so a mode only records which already-existing feed names it uses.
type ConfigMode struct {
	Name        string               `json:"name"`
	Enabled     bool                 `json:"enabled"`
	Feeds       []ConfigModeFeedLink `json:"feeds"`
	Communities []ConfigCommunity    `json:"communities"`
}

// ConfigUser is one user's configuration. BGPPassword is never exported —
// HasBGPPassword only says whether one is set — the same ethic this codebase
// already applies to cloning (see the #47 design notes on templates): a
// credential is never carried by a config operation. Networks, RouteFilters,
// and the selection are the user's own, in the mode named by CatalogMode.
type ConfigUser struct {
	Name                string       `json:"name"`
	PeerIP              string       `json:"peer_ip"`
	PeerASN             uint32       `json:"peer_asn"`
	NextHop             string       `json:"next_hop,omitempty"`
	Networks            []string     `json:"networks"`
	Enabled             bool         `json:"enabled"`
	SelectionLocked     bool         `json:"selection_locked"`
	FilterMode          string       `json:"filter_mode"`
	FilterEditable      bool         `json:"filter_editable"`
	CatalogMode         string       `json:"catalog_mode"`
	CatalogModeEditable bool         `json:"catalog_mode_editable"`
	ActiveDial          bool         `json:"active_dial"`
	WebAuth             string       `json:"web_auth"`
	HasBGPPassword      bool         `json:"has_bgp_password"`
	RouteFilters        RouteFilters `json:"route_filters"`
	SelectedCategories  []string     `json:"selected_categories"`
	SelectedServices    []ServiceKey `json:"selected_services"`
}

// ConfigSnapshot is the whole admin configuration this instance holds, in the
// scope issue #49 names: users, route filters, communities, and modes.
// Feeds, settings other than the global filters, and selection templates
// (which don't exist yet) are deliberately out of scope — see the #47/#49
// design notes.
type ConfigSnapshot struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedAt   int64        `json:"generated_at"`
	GlobalFilters RouteFilters `json:"global_filters"`
	Modes         []ConfigMode `json:"modes"`
	Users         []ConfigUser `json:"users"`
}

// ConfigSnapshot reads the whole admin configuration. Reads are not inside one
// transaction (unlike the community matrix, which needs cross-table point-in-
// time consistency for a correctness-sensitive grid): a config export is an
// infrequent, admin-triggered action, and a rare mid-export edit landing in
// one entity's favor is a cosmetic concern here, not a routing correctness one.
func (s *Store) ConfigSnapshot(ctx context.Context) (ConfigSnapshot, error) {
	snap := ConfigSnapshot{SchemaVersion: ConfigSnapshotSchemaVersion, GeneratedAt: time.Now().UTC().Unix()}
	globalFilters, err := s.GlobalRouteFilters(ctx)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	snap.GlobalFilters = globalFilters

	modes, err := s.CatalogModes(ctx, false)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	for _, mode := range modes {
		feeds, err := configModeFeedLinks(ctx, s.DB, mode.ID)
		if err != nil {
			return ConfigSnapshot{}, err
		}
		rows, err := s.CommunityRows(ctx, mode.ID)
		if err != nil {
			return ConfigSnapshot{}, err
		}
		communities := make([]ConfigCommunity, 0, len(rows))
		for _, r := range rows {
			communities = append(communities, ConfigCommunity{Category: r.Category, Service: r.Service, Community: r.Community})
		}
		snap.Modes = append(snap.Modes, ConfigMode{Name: mode.Name, Enabled: mode.Enabled, Feeds: feeds, Communities: communities})
	}

	users, err := s.Users(ctx, false)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	for _, u := range users {
		filters, err := s.UserRouteFilters(ctx, u.ID)
		if err != nil {
			return ConfigSnapshot{}, err
		}
		cats, svcs, err := userModeSelection(ctx, s.DB, u.ID, u.CatalogModeID)
		if err != nil {
			return ConfigSnapshot{}, err
		}
		catNames := make([]string, 0, len(cats))
		for c := range cats {
			catNames = append(catNames, c)
		}
		svcKeys := make([]ServiceKey, 0, len(svcs))
		for k := range svcs {
			svcKeys = append(svcKeys, k)
		}
		snap.Users = append(snap.Users, ConfigUser{
			Name:                u.Name,
			PeerIP:              u.PeerIP,
			PeerASN:             u.PeerASN,
			NextHop:             u.NextHop,
			Networks:            append([]string(nil), u.Networks...),
			Enabled:             u.Enabled,
			SelectionLocked:     u.SelectionLocked,
			FilterMode:          u.FilterMode,
			FilterEditable:      u.FilterEditable,
			CatalogMode:         u.CatalogModeName,
			CatalogModeEditable: u.CatalogEditable,
			ActiveDial:          u.ActiveDial,
			WebAuth:             u.WebAuth,
			HasBGPPassword:      u.BGPPassword != "",
			RouteFilters:        filters,
			SelectedCategories:  catNames,
			SelectedServices:    svcKeys,
		})
	}

	normalizeConfigSnapshot(&snap)
	return snap, nil
}

func configModeFeedLinks(ctx context.Context, q queryer, modeID int64) ([]ConfigModeFeedLink, error) {
	rows, err := q.QueryContext(ctx, `
SELECT f.name, cmf.exclude FROM catalog_mode_feeds cmf
JOIN feeds f ON f.id = cmf.feed_id
WHERE cmf.mode_id = ?
ORDER BY f.name`, modeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	var out []ConfigModeFeedLink
	for rows.Next() {
		var link ConfigModeFeedLink
		if err := rows.Scan(&link.Feed, &link.Exclude); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// normalizeConfigSnapshot sorts every list and turns a nil slice into an empty
// one, in place. Two independently-built snapshots of the same configuration
// must normalize identically, or an unrelated field order would show up as a
// spurious diff; it is also what lets ApplyConfigSnapshot trust a snapshot
// read from outside this package (JSON omits an empty array as null just as
// often as "[]").
func normalizeConfigSnapshot(snap *ConfigSnapshot) {
	if snap.Modes == nil {
		snap.Modes = []ConfigMode{}
	}
	if snap.Users == nil {
		snap.Users = []ConfigUser{}
	}
	normalizeRouteFilters(&snap.GlobalFilters)
	for i := range snap.Modes {
		m := &snap.Modes[i]
		if m.Feeds == nil {
			m.Feeds = []ConfigModeFeedLink{}
		}
		if m.Communities == nil {
			m.Communities = []ConfigCommunity{}
		}
		sort.Slice(m.Feeds, func(a, b int) bool { return m.Feeds[a].Feed < m.Feeds[b].Feed })
		sort.Slice(m.Communities, func(a, b int) bool {
			if m.Communities[a].Category != m.Communities[b].Category {
				return m.Communities[a].Category < m.Communities[b].Category
			}
			return m.Communities[a].Service < m.Communities[b].Service
		})
	}
	sort.Slice(snap.Modes, func(a, b int) bool { return snap.Modes[a].Name < snap.Modes[b].Name })
	for i := range snap.Users {
		u := &snap.Users[i]
		if u.Networks == nil {
			u.Networks = []string{}
		}
		if u.SelectedCategories == nil {
			u.SelectedCategories = []string{}
		}
		if u.SelectedServices == nil {
			u.SelectedServices = []ServiceKey{}
		}
		normalizeRouteFilters(&u.RouteFilters)
		sort.Strings(u.Networks)
		sort.Strings(u.SelectedCategories)
		sortServiceKeys(u.SelectedServices)
	}
	sort.Slice(snap.Users, func(a, b int) bool { return snap.Users[a].Name < snap.Users[b].Name })
}

func normalizeRouteFilters(f *RouteFilters) {
	if f.Allow == nil {
		f.Allow = []string{}
	}
	if f.Deny == nil {
		f.Deny = []string{}
	}
	sort.Strings(f.Allow)
	sort.Strings(f.Deny)
}

// Digest fingerprints snap's normalized content: two snapshots that differ
// only in field order, slice order, or nil-versus-empty slices produce the
// same digest. A single snapshot's own fingerprint, used by ConfigImportDigest
// below — not, on its own, what apiConfigImport confirms against, since it
// says nothing about what else might have changed in the meantime.
func (snap ConfigSnapshot) Digest() (string, error) {
	raw, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	var normalized ConfigSnapshot
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return "", err
	}
	normalizeConfigSnapshot(&normalized)
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// ConfigImportDigest fingerprints an import's full confirmation state: both
// the snapshot about to be applied and the live configuration it would be
// applied onto. Digest alone isn't enough for this — it reflects only the
// uploaded snapshot, so a preview's digest would still match at apply time
// even if another admin's edit changed the live target in between, letting
// an apply silently overwrite it. Binding both means a drifted target
// produces a different digest, and apiConfigImport refuses it with 409 —
// the same response as if the uploaded snapshot itself had changed.
func ConfigImportDigest(current, uploaded ConfigSnapshot) (string, error) {
	currentDigest, err := current.Digest()
	if err != nil {
		return "", err
	}
	uploadedDigest, err := uploaded.Digest()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(currentDigest + ":" + uploadedDigest))
	return hex.EncodeToString(sum[:]), nil
}

// ConfigChangedEntity is one entity present in both snapshots being diffed,
// with a value that differs between them.
type ConfigChangedEntity[T any] struct {
	Name   string `json:"name"`
	Before T      `json:"before"`
	After  T      `json:"after"`
}

// ConfigEntityDiff splits a list of named entities between two snapshots.
// Added and Changed are what an additive-only apply acts on; Removed is
// informational only — an entity present in Before but absent in After is
// never deleted by ApplyConfigSnapshot, so this is what would be skipped.
type ConfigEntityDiff[T any] struct {
	Added   []T                      `json:"added"`
	Removed []T                      `json:"removed"`
	Changed []ConfigChangedEntity[T] `json:"changed"`
}

// ConfigDiff is the structural difference between two configuration
// snapshots — DiffConfigSnapshots' result.
type ConfigDiff struct {
	GlobalFiltersChanged bool                         `json:"global_filters_changed"`
	GlobalFiltersBefore  RouteFilters                 `json:"global_filters_before"`
	GlobalFiltersAfter   RouteFilters                 `json:"global_filters_after"`
	Modes                ConfigEntityDiff[ConfigMode] `json:"modes"`
	Users                ConfigEntityDiff[ConfigUser] `json:"users"`
}

// DiffConfigSnapshots compares two configuration snapshots, however they were
// obtained — two exports from different instances, or one instance's export
// against an uploaded one being considered for import. It never touches the
// database: a pure function over its two arguments.
func DiffConfigSnapshots(before, after ConfigSnapshot) ConfigDiff {
	normalizeConfigSnapshot(&before)
	normalizeConfigSnapshot(&after)
	return ConfigDiff{
		GlobalFiltersChanged: !reflect.DeepEqual(before.GlobalFilters, after.GlobalFilters),
		GlobalFiltersBefore:  before.GlobalFilters,
		GlobalFiltersAfter:   after.GlobalFilters,
		Modes:                diffConfigEntities(before.Modes, after.Modes, func(m ConfigMode) string { return m.Name }),
		Users:                diffConfigEntities(before.Users, after.Users, func(u ConfigUser) string { return u.Name }),
	}
}

func diffConfigEntities[T any](before, after []T, key func(T) string) ConfigEntityDiff[T] {
	diff := ConfigEntityDiff[T]{Added: []T{}, Removed: []T{}, Changed: []ConfigChangedEntity[T]{}}
	beforeByName := map[string]T{}
	var beforeNames []string
	for _, e := range before {
		name := key(e)
		beforeByName[name] = e
		beforeNames = append(beforeNames, name)
	}
	afterByName := map[string]T{}
	var afterNames []string
	for _, e := range after {
		name := key(e)
		afterByName[name] = e
		afterNames = append(afterNames, name)
	}
	sort.Strings(afterNames)
	for _, name := range afterNames {
		a := afterByName[name]
		if b, ok := beforeByName[name]; ok {
			if !reflect.DeepEqual(b, a) {
				diff.Changed = append(diff.Changed, ConfigChangedEntity[T]{Name: name, Before: b, After: a})
			}
		} else {
			diff.Added = append(diff.Added, a)
		}
	}
	sort.Strings(beforeNames)
	for _, name := range beforeNames {
		if _, ok := afterByName[name]; !ok {
			diff.Removed = append(diff.Removed, beforeByName[name])
		}
	}
	return diff
}

// ConfigApplyResult reports what ApplyConfigSnapshot actually did.
type ConfigApplyResult struct {
	ModesCreated []string `json:"modes_created"`
	ModesUpdated []string `json:"modes_updated"`
	UsersCreated []string `json:"users_created"`
	UsersUpdated []string `json:"users_updated"`
	// UnknownFeeds and UnknownModes are "<entity>: <missing name>" entries for
	// a mode-feed link or a user's catalog mode that named something this
	// instance (and this import) has no match for. Skipped, not an error: one
	// broken reference must not fail everything else the import would apply.
	UnknownFeeds []string `json:"unknown_feeds"`
	UnknownModes []string `json:"unknown_modes"`
	// AffectedUserIDs is every user created or updated, for a caller outside
	// this package (apiConfigImport, in internal/web) that needs to resync
	// BGP peer state afterward — this package has no access to the BGP
	// manager, so it reports which users changed rather than acting on it.
	AffectedUserIDs []int64 `json:"-"`
}

// ApplyConfigSnapshot creates or updates the modes and users in snap. It is
// strictly additive at the entity level, per the decision this was built to:
// a mode or user this instance has that isn't in snap is left alone, never
// deleted, and dry-run diff (DiffConfigSnapshots) is always available first so
// that is visible before anything is applied. A user's or a new user's
// BGPPassword is never set or changed by it — ConfigSnapshot never exports it,
// so there is nothing to restore; a newly created user has none, and an admin
// must set one by hand.
//
// Within an entity snap does name, its own exported fields are synced to
// match snap exactly: a user's networks, route filters and selection, and a
// mode's feed membership, are replaced wholesale, the same as editing them by
// hand already does. Community assignments are the one exception — merged
// (upserted) rather than replaced, since a partial snapshot's community list
// isn't meant to be the sole authority over a mode's whole numbering, and
// wiping unlisted entries would have real BGP-community consequences.
func (s *Store) ApplyConfigSnapshot(ctx context.Context, snap ConfigSnapshot, meta AuditMeta) (ConfigApplyResult, error) {
	normalizeConfigSnapshot(&snap)
	result := ConfigApplyResult{}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		modeIDByName, err := catalogModeIDsByNameTx(ctx, tx)
		if err != nil {
			return err
		}
		feedIDByName, err := feedIDsByNameTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, m := range snap.Modes {
			modeID, existed := modeIDByName[m.Name]
			if !existed {
				res, err := tx.ExecContext(ctx, "INSERT INTO catalog_modes(name, enabled) VALUES (?, ?)", m.Name, m.Enabled)
				if err != nil {
					return err
				}
				modeID, err = res.LastInsertId()
				if err != nil {
					return err
				}
				modeIDByName[m.Name] = modeID
				result.ModesCreated = append(result.ModesCreated, m.Name)
			} else {
				if _, err := tx.ExecContext(ctx, "UPDATE catalog_modes SET enabled = ? WHERE id = ?", m.Enabled, modeID); err != nil {
					return err
				}
				result.ModesUpdated = append(result.ModesUpdated, m.Name)
			}
			var links []ModeFeedLink
			for _, f := range m.Feeds {
				feedID, ok := feedIDByName[f.Feed]
				if !ok {
					result.UnknownFeeds = append(result.UnknownFeeds, m.Name+": "+f.Feed)
					continue
				}
				links = append(links, ModeFeedLink{FeedID: feedID, Exclude: f.Exclude})
			}
			if err := replaceModeFeedsTx(ctx, tx, modeID, links); err != nil {
				return err
			}
			for _, c := range m.Communities {
				if err := setCommunityTx(ctx, tx, modeID, c.Category, c.Service, c.Community); err != nil {
					return err
				}
			}
		}

		userIDByName, err := userIDsByNameTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, u := range snap.Users {
			modeID, ok := modeIDByName[u.CatalogMode]
			if !ok {
				result.UnknownModes = append(result.UnknownModes, u.Name+": "+u.CatalogMode)
				continue
			}
			peerIP, nextHop, err := encodeUserAddrs(User{PeerIP: u.PeerIP, NextHop: u.NextHop})
			if err != nil {
				return err
			}
			userID, existed := userIDByName[u.Name]
			if !existed {
				res, err := tx.ExecContext(ctx, `INSERT INTO users
					(name, peer_ip, peer_asn, next_hop, bgp_password, selection_locked, enabled,
					 filter_mode, filter_editable, catalog_mode_id, catalog_mode_editable, active_dial, web_auth)
					VALUES (?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?)`,
					u.Name, peerIP, u.PeerASN, nextHop, u.SelectionLocked, u.Enabled,
					filterModeToInt(u.FilterMode), u.FilterEditable, modeID, u.CatalogModeEditable,
					u.ActiveDial, webAuthToInt(u.WebAuth))
				if err != nil {
					return err
				}
				userID, err = res.LastInsertId()
				if err != nil {
					return err
				}
				userIDByName[u.Name] = userID
				result.UsersCreated = append(result.UsersCreated, u.Name)
			} else {
				// bgp_password is deliberately absent from this list: the
				// snapshot never carries one, so an existing user's stays as is.
				if _, err := tx.ExecContext(ctx, `UPDATE users SET
					peer_ip = ?, peer_asn = ?, next_hop = ?, selection_locked = ?, enabled = ?,
					filter_mode = ?, filter_editable = ?, catalog_mode_id = ?, catalog_mode_editable = ?,
					active_dial = ?, web_auth = ?
					WHERE id = ?`,
					peerIP, u.PeerASN, nextHop, u.SelectionLocked, u.Enabled,
					filterModeToInt(u.FilterMode), u.FilterEditable, modeID, u.CatalogModeEditable,
					u.ActiveDial, webAuthToInt(u.WebAuth), userID); err != nil {
					return err
				}
				result.UsersUpdated = append(result.UsersUpdated, u.Name)
			}
			result.AffectedUserIDs = append(result.AffectedUserIDs, userID)
			// Same cross-user overlap check the admin user create/update
			// handlers enforce (internal/web's isActiveWebAuth gating) — an
			// import's networks must not silently let an IP-resolved user
			// shadow another active user's CIDRs. tx-scoped so it sees this
			// same import's own not-yet-committed writes to an earlier user
			// in this loop.
			if isActiveWebAuth(u.WebAuth) {
				if err := activeNetworksOverlapTx(ctx, tx, u.Networks, userID); err != nil {
					return fmt.Errorf("user %q: %w", u.Name, err)
				}
			}
			if err := replaceNetworks(ctx, tx, userID, u.Networks); err != nil {
				return err
			}
			// Not audited per user (AuditMeta{}): one summary entry for the
			// whole import, below, is the record of this action.
			if _, _, err := setUserRouteFiltersTx(ctx, tx, userID, u.RouteFilters, AuditMeta{}); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM selected_categories WHERE user_id = ? AND mode_id = ?", userID, modeID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM selected_services WHERE user_id = ? AND mode_id = ?", userID, modeID); err != nil {
				return err
			}
			for _, cat := range u.SelectedCategories {
				if err := ToggleSelectedCategory(ctx, tx, userID, modeID, cat, true); err != nil {
					return err
				}
			}
			for _, svc := range u.SelectedServices {
				if err := ToggleSelectedService(ctx, tx, userID, modeID, svc.Category, svc.Service, true); err != nil {
					return err
				}
			}
		}
		// force=true: a confirmed import is worth recording even if, on this
		// run, every entity already matched and nothing actually changed.
		return AuditEntryTx(ctx, tx, meta, "config", "import", nil, result, true)
	})
	return result, err
}

func catalogModeIDsByNameTx(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	return namesToIDsTx(ctx, tx, "SELECT id, name FROM catalog_modes")
}

func feedIDsByNameTx(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	return namesToIDsTx(ctx, tx, "SELECT id, name FROM feeds")
}

func userIDsByNameTx(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	return namesToIDsTx(ctx, tx, "SELECT id, name FROM users")
}

func namesToIDsTx(ctx context.Context, tx *sql.Tx, query string) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck
	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}
