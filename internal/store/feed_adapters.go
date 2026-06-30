package store

import (
	"context"
	"database/sql"
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

type builtInAdapter struct {
	name           string
	source         string
	builtinVersion int
	allowedHosts   string
}

var builtInAdapters = map[string]builtInAdapter{
	"canonical-json": {
		name: "Canonical JSON", source: canonicalJSONAdapter,
		builtinVersion: 1,
	},
	"opencck": {
		name: "OpenCCK", source: openCCKAdapter,
		builtinVersion: 1,
	},
	"ipranges": {
		name: "IPRanges", source: ipRangesAdapter,
		builtinVersion: 1,
		allowedHosts: "raw.githubusercontent.com",
	},
	"singbox-srs": {
		name: "sing-box SRS", source: singboxSRSAdapter,
		builtinVersion: 1,
	},
}

var builtInAdapterVersionByID = map[int64]int{}

func (s *Store) seedBuiltInAdapters(ctx context.Context) error {
	for key, adapter := range builtInAdapters {
		// First, INSERT OR IGNORE any built-in adapters that don't exist yet.
		if _, err := s.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO feed_adapters(key, name, language, api_version, source, builtin_version)
VALUES (?, ?, 'javascript', 1, ?, ?)`,
			key, adapter.name, normalizedBuiltInSource(adapter.source),
			adapter.builtinVersion); err != nil {
			return err
		}
		// Capture the adapter ID for the version map (regardless of whether it was just inserted or already existed).
		var id int64
		if err := s.DB.QueryRowContext(ctx, "SELECT id FROM feed_adapters WHERE key = ?", key).Scan(&id); err == nil {
			builtInAdapterVersionByID[id] = adapter.builtinVersion
		}
		// Then, read current state of the adapter.
		var isCustomized, builtinVersion int
		var storedSource, currentName string
		err := s.DB.QueryRowContext(ctx,
			"SELECT is_customized, builtin_version, source, name FROM feed_adapters WHERE key = ?", key).
			Scan(&isCustomized, &builtinVersion, &storedSource, &currentName)
		if err != nil {
			continue
		}

		normBuiltIn := normalizedBuiltInSource(adapter.source)

		if isCustomized == 1 { //nolint:gocritic // if/else chain is clearer than switch for this logic
			// Only update builtin_version; preserve user edits.
			if _, err := s.DB.ExecContext(ctx,
				"UPDATE feed_adapters SET builtin_version = ? WHERE key = ?",
				adapter.builtinVersion, key); err != nil {
				return err
			}
			builtInAdapterVersionByID[id] = adapter.builtinVersion
		} else if builtinVersion == 0 && storedSource != "" && (strings.TrimSpace(storedSource) != strings.TrimSpace(normBuiltIn) ||
			currentName != adapter.name) {
			// Freshly migrated adapter (builtin_version == 0) whose source
			// differs from the built-in default. This adapter was customized
			// before migration 20 added the is_customized column.
			// Mark as customized so future seed runs don't overwrite it.
			if _, err := s.DB.ExecContext(ctx,
				"UPDATE feed_adapters SET is_customized = 1, builtin_version = ? WHERE key = ?",
				adapter.builtinVersion, key); err != nil {
				return err
			}
			builtInAdapterVersionByID[id] = adapter.builtinVersion
		} else {
			// Not customized: update name, source, builtin_version.
			if _, err := s.DB.ExecContext(ctx, `
UPDATE feed_adapters
SET name = ?, source = ?, builtin_version = ?
WHERE key = ?`,
				adapter.name, normBuiltIn,
				adapter.builtinVersion, key); err != nil {
				return err
			}
			builtInAdapterVersionByID[id] = adapter.builtinVersion
		}

		// Set forked_version for built-ins that have it unset.
		// Must run after builtin_version is updated above.
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE feed_adapters SET forked_version = builtin_version WHERE key = ? AND forked_version = 0`,
			key); err != nil {
			return err
		}
	}
	return nil
}

// ForkedAdapterNeedsReview returns true when the built-in adapter that a fork
// was created from has been updated since the fork was made.
func ForkedAdapterNeedsReview(forkedFromID int64, forkedVersion int64) bool {
	if forkedFromID == 0 {
		return false
	}
	builtinVer, ok := builtInAdapterVersionByID[forkedFromID]
	if !ok {
		return false
	}
	return int64(builtinVer) > forkedVersion
}

// BuiltInAdapterVersion returns the current builtin version for a given adapter ID.
func BuiltInAdapterVersion(id int64) (int64, bool) {
	v, ok := builtInAdapterVersionByID[id]
	return int64(v), ok
}

func (s *Store) ResetFeedAdapter(ctx context.Context, id int64) error {
	var key string
	if err := s.DB.QueryRowContext(ctx,
		"SELECT key FROM feed_adapters WHERE id = ?", id).Scan(&key); err != nil {
		return err
	}
	adapter, ok := builtInAdapters[key]
	if !ok {
		return fmt.Errorf("adapter %q is not built in", key)
	}
	result, err := s.DB.ExecContext(ctx, `
UPDATE feed_adapters
SET name = ?, source = ?, is_customized = 0, revision = revision + 1
WHERE id = ?`,
		adapter.name, normalizedBuiltInSource(adapter.source),
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
// a built-in adapter, or empty string if the adapter is not built-in or has none.
func (s *Store) BuiltinAdapterAllowedHosts(ctx context.Context, adapterID int64) string {
	for _, key := range []string{"canonical-json", "opencck", "ipranges", "singbox-srs"} {
		ba := builtInAdapters[key]
		var id int64
		if err := s.DB.QueryRowContext(ctx, "SELECT id FROM feed_adapters WHERE key = ?", key).Scan(&id); err == nil && id == adapterID {
			return ba.allowedHosts
		}
	}
	return ""
}

func (s *Store) AddFeedAdapter(ctx context.Context, adapter FeedAdapter) (FeedAdapter, error) {
	key := adapter.Key
	if key == "" {
		// Auto-generate a key from the name when not provided (e.g., forked adapters).
		key = strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r + 32 // lowercase
			}
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
				return r
			}
			return '-'
		}, adapter.Name)
	}
	var a FeedAdapter
	err := s.DB.QueryRowContext(ctx, `
INSERT INTO feed_adapters(key, name, language, api_version, source, forked_from, forked_version, is_builtin)
VALUES (?, ?, 'javascript', 1, ?, NULLIF(?, 0), ?, 0)
RETURNING id, key, name, language, api_version, source, revision, is_builtin, COALESCE(forked_from, 0), forked_version`,

		key, adapter.Name, adapter.Source, adapter.ForkedFrom, adapter.ForkedVersion,
	).Scan(&a.ID, &a.Key, &a.Name, &a.Language, &a.APIVersion, &a.Source, &a.Revision, &a.BuiltIn, &a.ForkedFrom, &a.ForkedVersion)
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
			if v, ok := BuiltInAdapterVersion(src.ID); ok {
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
SELECT id, key, name, language, api_version, source, revision, COALESCE(forked_from, 0), forked_version
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
		if err := rows.Scan(&a.ID, &a.Key, &a.Name, &a.Language, &a.APIVersion,
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
