package migrations

import (
	"context"
	"database/sql"
)

func V019(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO catalog_modes(key, name, enabled) VALUES ('singbox-srs', 'sing-box SRS', 0);
UPDATE catalog_modes SET name = 'sing-box SRS' WHERE key = 'singbox-srs';
INSERT OR IGNORE INTO feed_adapters(key, name) VALUES ('singbox-srs', 'sing-box SRS');
UPDATE feed_adapters SET name = 'sing-box SRS' WHERE key = 'singbox-srs' AND source = '';
INSERT OR IGNORE INTO feeds(name, url, mode_id, adapter_id, enabled, sync_interval, data)
SELECT 'Russia GeoIP (SRS)',
       'https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box/rule-set-geoip/geoip-ru.srs',
       (SELECT id FROM catalog_modes WHERE key = 'singbox-srs'),
       (SELECT id FROM feed_adapters WHERE key = 'singbox-srs'),
       0, 0,
       '{"category":"Russia","service":"geoip-ru"}'
WHERE EXISTS (SELECT 1 FROM catalog_modes WHERE key = 'singbox-srs')
  AND EXISTS (SELECT 1 FROM feed_adapters WHERE key = 'singbox-srs');
`)
	return err
}
