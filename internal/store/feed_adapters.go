package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
)

const canonicalJSONAdapter = `
function sync(feed, api) {
    var value = JSON.parse(api.httpGet(feed.url));
    if (Array.isArray(value)) {
        return value;
    }
    if (value && Array.isArray(value.entries)) {
        return value.entries;
    }
    return [value];
}
`

const openCCKAdapter = `
function sync(feed, api) {
    var data = JSON.parse(api.httpGet(feed.url));
    var metadataURL = feed.url.replace(
        /([?&])data=cidr[46](&|$)/,
        "$1data=group$2"
    );
    var groups = metadataURL === feed.url
        ? {}
        : JSON.parse(api.httpGet(metadataURL));
    var result = [];

    Object.keys(data).forEach(function (key) {
        var item = data[key];
        if (item && typeof item === "object" && !Array.isArray(item)) {
            result.push({
                category: item.group,
                service: item.name || key,
                cidrs: (item.cidr4 || []).concat(item.cidr6 || [])
            });
            return;
        }
        result.push({
            category: groups[key] || feed.name,
            service: key,
            cidrs: item
        });
    });
    return result;
}
`

const singboxSRSAdapter = `function sync(feed, api) {
    var cfg = (feed.data && JSON.parse(feed.data)) || {};
    var entries = api.srsGet(feed.url, JSON.stringify({cidrs: cfg.cidrs !== false}));
    var cat = cfg.category || feed.name || "srs";
    var svc = cfg.service || "srs";
    return entries.map(function(e) {
        return { category: cat, service: svc, cidrs: e.cidrs };
    });
}`

const ipRangesAdapter = `
var services = [
    ["M247", "Infrastructure", "M247", true],
    ["adobe", "Platforms", "Adobe", true],
    ["akamai", "Infrastructure", "Akamai", true],
    ["alibaba", "Platforms", "Alibaba", true],
    ["amazon", "Cloud providers", "Amazon AWS", true],
    ["apple", "Platforms", "Apple", false],
    ["apple-proxy", "Privacy", "Apple Private Relay", true],
    ["avito", "Platforms", "Avito", true],
    ["azure", "Cloud providers", "Microsoft Azure", true],
    ["backblaze", "Cloud providers", "Backblaze", true],
    ["beeline", "Networks", "Beeline", false],
    ["bing", "Platforms", "Bing", false],
    ["cachefly", "Infrastructure", "CacheFly", true],
    ["cloudflare", "Infrastructure", "Cloudflare", true],
    ["constant", "Infrastructure", "Constant", true],
    ["corbina", "Networks", "Corbina", false],
    ["digitalocean", "Cloud providers", "DigitalOcean", true],
    ["edgecast", "Infrastructure", "EdgeCast", true],
    ["expressvpn", "Privacy", "ExpressVPN", true],
    ["facebook", "Platforms", "Facebook", true],
    ["fastly", "Infrastructure", "Fastly", true],
    ["github", "Platforms", "GitHub", true],
    ["google", "Platforms", "Google", true],
    ["googlecloud", "Cloud providers", "Google Cloud", true],
    ["hetzner", "Cloud providers", "Hetzner", true],
    ["hostinger", "Cloud providers", "Hostinger", true],
    ["huggingface", "Platforms", "Hugging Face", false],
    ["imperva", "Infrastructure", "Imperva", true],
    ["kinopub", "Platforms", "Kinopub", true],
    ["linode", "Cloud providers", "Linode", true],
    ["microsoft", "Platforms", "Microsoft", true],
    ["mts", "Networks", "MTS", true],
    ["mtscloud", "Cloud providers", "MTS Cloud", false],
    ["mullvad", "Privacy", "Mullvad", true],
    ["nordvpn", "Privacy", "NordVPN", true],
    ["oracle", "Cloud providers", "Oracle Cloud", false],
    ["ovh", "Cloud providers", "OVHcloud", true],
    ["ozonru", "Platforms", "Ozon", true],
    ["pia", "Privacy", "Private Internet Access", false],
    ["protonvpn", "Privacy", "ProtonVPN", true],
    ["qrator", "Infrastructure", "Qrator", true],
    ["rambler", "Platforms", "Rambler", true],
    ["rostelecom", "Networks", "Rostelecom", true],
    ["rugov", "Government", "Russian government sites", true],
    ["sber", "Platforms", "Sber", true],
    ["surfshark", "Privacy", "Surfshark", true],
    ["telegram", "Platforms", "Telegram", true],
    ["tiktok", "Platforms", "TikTok", true],
    ["twitter", "Platforms", "Twitter / X", true],
    ["vercel", "Infrastructure", "Vercel", false],
    ["vkontakte", "Platforms", "VKontakte", true],
    ["vpnhosts", "Privacy", "Popular VPN hosts", true],
    ["yahoo", "Platforms", "Yahoo", true],
    ["yandex", "Platforms", "Yandex", true],
    ["yandexcloud", "Cloud providers", "Yandex Cloud", true],
    ["youtube", "Platforms", "YouTube", false]
];

function sync(feed, api) {
    var result = [];
    services.forEach(function (item) {
        var families = item[3] ? ["ipv4", "ipv6"] : ["ipv4"];
        families.forEach(function (family) {
            var url = "https://raw.githubusercontent.com/antonme/ipranges/main/"
                + item[0] + "/" + family + "_merged.txt";
            result.push({
                category: item[1],
                service: item[2],
                cidrs: api.httpGet(url).split(/\s+/).filter(Boolean)
            });
        });
    });
    return result;
}
`

const asAdapter = `
function sync(feed, api) {
    if (!feed.data || !feed.data.trim()) {
        throw new Error("AS adapter: feed Data JSON is required ({asns, category, service})");
    }
    var cfg;
    try {
        cfg = JSON.parse(feed.data);
    } catch (e) {
        throw new Error("AS adapter: feed Data is not valid JSON: " + e.message);
    }
    if (!Array.isArray(cfg.asns) || cfg.asns.length === 0) {
        throw new Error("AS adapter: 'asns' must be a non-empty array of AS numbers");
    }
    cfg.asns.forEach(function (asn) {
        if (typeof asn !== "number" || !isFinite(asn) || asn <= 0 || asn !== Math.floor(asn)) {
            throw new Error("AS adapter: 'asns' contains an invalid AS number: " + JSON.stringify(asn));
        }
    });
    if (typeof cfg.category !== "string" || !cfg.category.trim()) {
        throw new Error("AS adapter: 'category' must be a non-empty string");
    }
    if (typeof cfg.service !== "string" || !cfg.service.trim()) {
        throw new Error("AS adapter: 'service' must be a non-empty string");
    }

    var seen = {};
    var cidrs = [];
    cfg.asns.forEach(function (asn) {
        var url = "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS"
            + asn + "&sourceapp=wdbgp";
        var resp = JSON.parse(api.httpGet(url));
        if (resp.status !== "ok" || !resp.data || !Array.isArray(resp.data.prefixes)) {
            throw new Error("AS adapter: RIPEstat returned status " + JSON.stringify(resp.status) + " for AS" + asn);
        }
        var prefixes = resp.data.prefixes;
        api.log("AS" + asn + ": " + prefixes.length + " announced prefixes");
        prefixes.forEach(function (p) {
            if (p && p.prefix && !seen[p.prefix]) {
                seen[p.prefix] = true;
                cidrs.push(p.prefix);
            }
        });
    });

    return [{
        category: cfg.category,
        service: cfg.service,
        cidrs: cidrs
    }];
}
`

// builtInAdapter is keyed by the adapter's canonical name (feed_adapters.name
// is NOT NULL UNIQUE) — combined with is_builtin, that's already a unique,
// stable identifier for "which built-in is this DB row," so there's no need
// for a separate slug/key column purely for that join.
type builtInAdapter struct {
	source         string
	builtinVersion int
	allowedHosts   string
}

var builtInAdapters = map[string]builtInAdapter{
	"Canonical JSON": {
		source:         canonicalJSONAdapter,
		builtinVersion: 1,
	},
	"OpenCCK": {
		source:         openCCKAdapter,
		builtinVersion: 1,
	},
	"IPRanges": {
		source:         ipRangesAdapter,
		builtinVersion: 1,
		allowedHosts:   "raw.githubusercontent.com",
	},
	"sing-box SRS": {
		source:         singboxSRSAdapter,
		builtinVersion: 1,
	},
	"ASN": {
		source:         asAdapter,
		builtinVersion: 1,
		allowedHosts:   "stat.ripe.net",
	},
}

// setBuiltInAdapterVersion records the current builtin version for an
// adapter ID. Guarded by builtInMu since this map is per-Store but can still
// be read (ForkAdapter, adapter list rendering) from a different goroutine
// than the one seeding it.
func (s *Store) setBuiltInAdapterVersion(id int64, version int) {
	s.builtInMu.Lock()
	defer s.builtInMu.Unlock()
	s.builtInAdapterVersionByID[id] = version
}

// freeUpBuiltinName renames any pre-existing non-builtin adapter occupying
// name, so a builtin can claim it below. Feeds reference adapters by id, so
// the rename can never break an existing feed -> adapter link — it only
// changes what the row is called. Without this, seeding a new builtin whose
// name collides with a user's custom adapter would either silently skip
// creating the builtin (INSERT OR IGNORE) or, worse, adopt the user's row
// and reinterpret it as the builtin it never was.
func (s *Store) freeUpBuiltinName(ctx context.Context, name string) error {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		"SELECT id FROM feed_adapters WHERE name = ? AND is_builtin = 0", name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	newName, err := s.nextAvailableAdapterName(ctx, name+" (custom)")
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx,
		"UPDATE feed_adapters SET name = ? WHERE id = ?", newName, id); err != nil {
		return err
	}
	log.Printf("renamed custom feed adapter %q to %q to make room for builtin %q", name, newName, name)
	return nil
}

// nextAvailableAdapterName returns base, or base with a growing numeric
// suffix if base is already taken.
func (s *Store) nextAvailableAdapterName(ctx context.Context, base string) (string, error) {
	name := base
	for i := 2; ; i++ {
		var exists int
		if err := s.DB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM feed_adapters WHERE name = ?", name).Scan(&exists); err != nil {
			return "", err
		}
		if exists == 0 {
			return name, nil
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
}

func (s *Store) seedBuiltInAdapters(ctx context.Context) error {
	for name, adapter := range builtInAdapters {
		if err := s.freeUpBuiltinName(ctx, name); err != nil {
			return err
		}
		// First, INSERT OR IGNORE any built-in adapters that don't exist
		// yet. is_builtin is set explicitly here (not left to a one-time
		// migration backfill) so a built-in added to this map in a future
		// release is correctly flagged from the moment it's first seeded.
		if _, err := s.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO feed_adapters(name, language, api_version, source, builtin_version, is_builtin)
VALUES (?, 'javascript', 1, ?, ?, 1)`,
			name, normalizedBuiltInSource(adapter.source),
			adapter.builtinVersion); err != nil {
			return err
		}
		// Capture the adapter ID for the version map (regardless of whether it was just inserted or already existed).
		var id int64
		if err := s.DB.QueryRowContext(ctx, "SELECT id FROM feed_adapters WHERE name = ?", name).Scan(&id); err == nil {
			s.setBuiltInAdapterVersion(id, adapter.builtinVersion)
		}
		// Then, read current state of the adapter.
		var isCustomized, builtinVersion int
		var storedSource string
		err := s.DB.QueryRowContext(ctx,
			"SELECT is_customized, builtin_version, source FROM feed_adapters WHERE name = ?", name).
			Scan(&isCustomized, &builtinVersion, &storedSource)
		if err != nil {
			continue
		}

		normBuiltIn := normalizedBuiltInSource(adapter.source)

		if isCustomized == 1 { //nolint:gocritic // if/else chain is clearer than switch for this logic
			// Only update builtin_version; preserve user edits.
			if _, err := s.DB.ExecContext(ctx,
				"UPDATE feed_adapters SET builtin_version = ? WHERE name = ?",
				adapter.builtinVersion, name); err != nil {
				return err
			}
			s.setBuiltInAdapterVersion(id, adapter.builtinVersion)
		} else if builtinVersion == 0 && storedSource != "" && strings.TrimSpace(storedSource) != strings.TrimSpace(normBuiltIn) {
			// Freshly migrated adapter (builtin_version == 0) whose source
			// differs from the built-in default. This adapter was customized
			// before migration 20 added the is_customized column.
			// Mark as customized so future seed runs don't overwrite it.
			if _, err := s.DB.ExecContext(ctx,
				"UPDATE feed_adapters SET is_customized = 1, builtin_version = ? WHERE name = ?",
				adapter.builtinVersion, name); err != nil {
				return err
			}
			s.setBuiltInAdapterVersion(id, adapter.builtinVersion)
		} else {
			// Not customized: update source, builtin_version. name is
			// already correct — it's how this row was found above.
			if _, err := s.DB.ExecContext(ctx, `
UPDATE feed_adapters
SET source = ?, builtin_version = ?
WHERE name = ?`,
				normBuiltIn,
				adapter.builtinVersion, name); err != nil {
				return err
			}
			s.setBuiltInAdapterVersion(id, adapter.builtinVersion)
		}

		// Set forked_version for built-ins that have it unset.
		// Must run after builtin_version is updated above.
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE feed_adapters SET forked_version = builtin_version WHERE name = ? AND forked_version = 0`,
			name); err != nil {
			return err
		}
	}
	return nil
}

// ForkedAdapterNeedsReview returns true when the built-in adapter that a fork
// was created from has been updated since the fork was made.
func (s *Store) ForkedAdapterNeedsReview(forkedFromID int64, forkedVersion int64) bool {
	if forkedFromID == 0 {
		return false
	}
	builtinVer, ok := s.BuiltInAdapterVersion(forkedFromID)
	if !ok {
		return false
	}
	return builtinVer > forkedVersion
}

// BuiltInAdapterVersion returns the current builtin version for a given adapter ID.
func (s *Store) BuiltInAdapterVersion(id int64) (int64, bool) {
	s.builtInMu.RLock()
	defer s.builtInMu.RUnlock()
	v, ok := s.builtInAdapterVersionByID[id]
	return int64(v), ok
}

func (s *Store) ResetFeedAdapter(ctx context.Context, id int64) error {
	var name string
	if err := s.DB.QueryRowContext(ctx,
		"SELECT name FROM feed_adapters WHERE id = ?", id).Scan(&name); err != nil {
		return err
	}
	adapter, ok := builtInAdapters[name]
	if !ok {
		return fmt.Errorf("adapter %q is not built in", name)
	}
	result, err := s.DB.ExecContext(ctx, `
UPDATE feed_adapters
SET source = ?, is_customized = 0, revision = revision + 1
WHERE id = ?`,
		normalizedBuiltInSource(adapter.source),
		id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func normalizedBuiltInSource(source string) string {
	return strings.TrimSpace(source) + "\n"
}

// BuiltinAdapterAllowedHosts returns the additional allowed hosts declared by
// a built-in adapter, or empty string if the adapter is not built-in (or a
// fork of one) or has none. A fork's source still calls out to the same
// hosts as the built-in it was copied from, so forked_from is resolved to
// the underlying built-in adapter before matching — otherwise a feed using
// a fork of e.g. the IPRanges adapter would default to restrict_hosts=true
// with no extra hosts and reject its own requests to raw.githubusercontent.com.
func (s *Store) BuiltinAdapterAllowedHosts(ctx context.Context, adapterID int64) string {
	var forkedFrom int64
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(forked_from, 0) FROM feed_adapters WHERE id = ?", adapterID).Scan(&forkedFrom); err == nil && forkedFrom != 0 {
		adapterID = forkedFrom
	}
	for name, ba := range builtInAdapters {
		var id int64
		if err := s.DB.QueryRowContext(ctx, "SELECT id FROM feed_adapters WHERE name = ?", name).Scan(&id); err == nil && id == adapterID {
			return ba.allowedHosts
		}
	}
	return ""
}

func (s *Store) AddFeedAdapter(ctx context.Context, adapter FeedAdapter) (FeedAdapter, error) {
	var a FeedAdapter
	err := s.DB.QueryRowContext(ctx, `
INSERT INTO feed_adapters(name, language, api_version, source, forked_from, forked_version, is_builtin)
VALUES (?, 'javascript', 1, ?, NULLIF(?, 0), ?, 0)
RETURNING id, name, language, api_version, source, revision, is_builtin, COALESCE(forked_from, 0), forked_version`,

		adapter.Name, adapter.Source, adapter.ForkedFrom, adapter.ForkedVersion,
	).Scan(&a.ID, &a.Name, &a.Language, &a.APIVersion, &a.Source, &a.Revision, &a.BuiltIn, &a.ForkedFrom, &a.ForkedVersion)
	if err != nil {
		return FeedAdapter{}, err
	}
	return a, nil
}

// ForkAdapter creates a copy of the adapter with auto-naming (_copy_N suffix).
func (s *Store) ForkAdapter(ctx context.Context, sourceID int64) (FeedAdapter, error) {
	src, err := s.FeedAdapter(ctx, sourceID)
	if err != nil {
		return FeedAdapter{}, fmt.Errorf("load source adapter: %w", err)
	}
	suffix, err := s.maxForkedAdapterSuffix(ctx, src.ID)
	if err != nil {
		return FeedAdapter{}, fmt.Errorf("compute fork suffix: %w", err)
	}
	fork := FeedAdapter{
		Name:       fmt.Sprintf("%s_copy_%d", src.Name, suffix+1),
		Source:     src.Source,
		Language:   src.Language,
		APIVersion: src.APIVersion,
		ForkedFrom: src.ID,
		ForkedVersion: func() int64 {
			if v, ok := s.BuiltInAdapterVersion(src.ID); ok {
				return v
			}
			return src.Revision
		}(),
	}
	return s.AddFeedAdapter(ctx, fork)
}

// DeleteFeedAdapter deletes an adapter. Fails if any feeds reference it (FK constraint).
func (s *Store) DeleteFeedAdapter(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM feed_adapters WHERE id = ?", id)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("rows affected: %w", err)
	} else if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateFeedAdapter(ctx context.Context, adapter FeedAdapter) error {
	result, err := s.DB.ExecContext(ctx, `
UPDATE feed_adapters
SET name = ?, source = ?, revision = revision + 1, is_customized = 1,
    forked_version = ?
WHERE id = ?`,
		adapter.Name, adapter.Source, adapter.ForkedVersion, adapter.ID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ValidateFeedAdapter(adapter FeedAdapter) error {
	if strings.TrimSpace(adapter.Name) == "" {
		return fmt.Errorf("adapter name is required")
	}
	if strings.TrimSpace(adapter.Source) == "" {
		return fmt.Errorf("adapter source is required")
	}
	return nil
}

func (s *Store) ForkedAdaptersByKey(ctx context.Context, forkedFromID int64) ([]FeedAdapter, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, name, language, api_version, source, revision, COALESCE(forked_from, 0), forked_version
FROM feed_adapters
WHERE forked_from = ?
ORDER BY name`, forkedFromID)
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
		var a FeedAdapter
		if err := rows.Scan(&a.ID, &a.Name, &a.Language, &a.APIVersion,
			&a.Source, &a.Revision, &a.ForkedFrom, &a.ForkedVersion); err != nil {
			return nil, err
		}
		adapters = append(adapters, a)
	}
	return adapters, rows.Err()
}

func (s *Store) maxForkedAdapterSuffix(ctx context.Context, forkedFromID int64) (int, error) {
	var isBuiltin bool
	var srcName string
	if err := s.DB.QueryRowContext(ctx,
		"SELECT is_builtin, name FROM feed_adapters WHERE id = ?", forkedFromID,
	).Scan(&isBuiltin, &srcName); err != nil {
		return 0, err
	}
	if !isBuiltin {
		return 0, fmt.Errorf("adapter %d is not built-in", forkedFromID)
	}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT name FROM feed_adapters WHERE forked_from = ? AND name LIKE ?",
		forkedFromID, srcName+"_copy_%")
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("WARNING: rows close: %v", err)
		}
	}()
	maxSuffix := 0
	prefix := "_copy_"
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return 0, err
		}
		idx := strings.LastIndex(name, prefix)
		if idx >= 0 {
			numStr := name[idx+len(prefix):]
			if n, err := strconv.Atoi(numStr); err == nil && n > maxSuffix {
				maxSuffix = n
			}
		}
	}
	return maxSuffix, rows.Err()
}
