package store

import (
	"context"
	"database/sql"
	"fmt"
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
	name         string
	source       string
	allowedHosts string
	builtinVersion int
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
		allowedHosts: "raw.githubusercontent.com",
		builtinVersion: 1,
	},
	"singbox-srs": {
		name: "sing-box SRS", source: singboxSRSAdapter,
		builtinVersion: 1,
	},
}

func (s *Store) seedBuiltInAdapters(ctx context.Context) error {
	for key, adapter := range builtInAdapters {
		// First, INSERT OR IGNORE any built-in adapters that don't exist yet.
		if _, err := s.DB.ExecContext(ctx, `
INSERT OR IGNORE INTO feed_adapters(key, name, language, api_version, source, allowed_hosts, builtin_version)
VALUES (?, ?, 'javascript', 1, ?, ?, ?)`,
			key, adapter.name, normalizedBuiltInSource(adapter.source),
			adapter.allowedHosts, adapter.builtinVersion); err != nil {
			return err
		}
		// Then, read current state of the adapter.
		var isCustomized, builtinVersion int
		var storedSource, currentName, currentAllowedHosts string
		err := s.DB.QueryRowContext(ctx,
			"SELECT is_customized, builtin_version, source, name, COALESCE(allowed_hosts, '') FROM feed_adapters WHERE key = ?", key).
			Scan(&isCustomized, &builtinVersion, &storedSource, &currentName, &currentAllowedHosts)
		if err != nil {
			continue
		}

		normBuiltIn := normalizedBuiltInSource(adapter.source)

		if isCustomized == 1 {
			// Only update builtin_version; preserve user edits.
			if _, err := s.DB.ExecContext(ctx,
				"UPDATE feed_adapters SET builtin_version = ? WHERE key = ?",
				adapter.builtinVersion, key); err != nil {
				return err
			}
		} else if builtinVersion == 0 && storedSource != "" && (strings.TrimSpace(storedSource) != strings.TrimSpace(normBuiltIn) ||
			currentName != adapter.name ||
			currentAllowedHosts != adapter.allowedHosts) {
			// Freshly migrated adapter (builtin_version == 0) whose source
			// differs from the built-in default. This adapter was customized
			// before migration 20 added the is_customized column.
			// Mark as customized so future seed runs don't overwrite it.
			if _, err := s.DB.ExecContext(ctx,
				"UPDATE feed_adapters SET is_customized = 1, builtin_version = ? WHERE key = ?",
				adapter.builtinVersion, key); err != nil {
				return err
			}
		} else {
			// Not customized: update name, source, allowed_hosts, builtin_version.
			if _, err := s.DB.ExecContext(ctx, `
UPDATE feed_adapters
SET name = ?, source = ?, allowed_hosts = ?, builtin_version = ?
WHERE key = ?`,
				adapter.name, normBuiltIn,
				adapter.allowedHosts, adapter.builtinVersion, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func IsBuiltInFeedAdapter(key string) bool {
	_, ok := builtInAdapters[key]
	return ok
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
SET name = ?, source = ?, allowed_hosts = ?, is_customized = 0, revision = revision + 1
WHERE id = ?`,
		adapter.name, normalizedBuiltInSource(adapter.source),
		adapter.allowedHosts, id)
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

func (s *Store) AddFeedAdapter(ctx context.Context, adapter FeedAdapter) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `
INSERT INTO feed_adapters(key, name, language, api_version, source, allowed_hosts)
VALUES (?, ?, 'javascript', 1, ?, ?)`,
		adapter.Key, adapter.Name, adapter.Source, adapter.AllowedHosts)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// DeleteFeedAdapter deletes an adapter. Fails if any feeds reference it (FK constraint).
func (s *Store) DeleteFeedAdapter(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM feed_adapters WHERE id = ?", id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateFeedAdapter(ctx context.Context, adapter FeedAdapter) error {
	result, err := s.DB.ExecContext(ctx, `
UPDATE feed_adapters
SET name = ?, source = ?, allowed_hosts = ?, revision = revision + 1, is_customized = 1
WHERE id = ?`,
		adapter.Name, adapter.Source, adapter.AllowedHosts, adapter.ID)
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
	if adapter.ID == 0 {
		key := strings.TrimSpace(adapter.Key)
		if key == "" {
			return fmt.Errorf("adapter key is required")
		}
		for _, character := range key {
			if character >= 'a' && character <= 'z' ||
				character >= '0' && character <= '9' ||
				character == '.' || character == '_' || character == '-' {
				continue
			}
			return fmt.Errorf("adapter key contains unsupported character %q", character)
		}
	}
	if strings.TrimSpace(adapter.Name) == "" {
		return fmt.Errorf("adapter name is required")
	}
	if strings.TrimSpace(adapter.Source) == "" {
		return fmt.Errorf("adapter source is required")
	}
	return nil
}
