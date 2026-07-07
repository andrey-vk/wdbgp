export interface SettingMeta {
  label: string
  hint: string
  type: 'bool' | 'number' | 'string' | 'select' | 'textarea' | 'password'
  restart?: boolean
  readonly?: boolean                  // always read-only — set via envVar only, no UI/API edit path at all
  options?: Record<string, string>  // value → i18n label key (for select only)
  envVar?: string                     // for display only (env tag)
}

export interface SettingsSection {
  name: string          // i18n key for section title
  fields: Record<string, SettingMeta>  // keyed by settings field name
}

// Default values displayed when no override exists (display purposes only)
// Values match backend defaults from internal/settings/settings.go
export const defaults: Record<string, string> = {
  active_dial: 'false',
  adapter_backup_dir: '',
  adapter_backup_max: '10',
  admin_cookie_secure: 'auto',
  admin_password: '',
  allow_dynamic_peers: 'false',
  auto_restore_enabled: 'false',
  bgp_port: '179',
  backup_dir: '',
  backup_enabled: 'true',
  db_path: '/data/wdbgp.sqlite3',
  default_language: 'en',
  default_web_auth: 'network',
  dynamic_peer_md5_match: 'false',
  dynamic_peer_md5_queue_num: '0',
  host: '0.0.0.0',
  js_max_call_stack: '1000',
  js_max_entries: '1000000',
  js_max_requests: '200',
  js_max_response: '16777216',
  js_max_source: '1048576',
  js_max_total: '67108864',
  js_timeout: '120',
  local_asn: '64512',
  local_address_v4: '192.0.2.2',
  local_address_v6: '',
  log_format: 'text',
  log_level: 'INFO',
  metrics_enabled: 'false',
  metrics_history_days: '14',
  port: '8080',
  rate_limit_admin: '30',
  rate_limit_login: '5',
  require_password_for_non_unique_ip: 'true',
  router_id: '192.0.2.1',
  security_headers: 'false',
  session_max_age: '28800',
  session_secret: '',
  status_allowed: '',
  status_token: '',
  sync_interval: '3600',
  trust_proxy_headers: 'false',
}

export const sections: SettingsSection[] = [
  {
    name: 'settings.section_admin_access',
    fields: {
      admin_password:     { label: 'settings.admin_password',      hint: 'settings.admin_password_hint',      type: 'password', envVar: 'WDBGP_ADMIN_PASSWORD' },
      session_secret:     { label: 'settings.session_secret',      hint: 'settings.session_secret_hint',      type: 'password', envVar: 'WDBGP_SESSION_SECRET' },
      session_max_age:    { label: 'settings.session_max_age',     hint: 'settings.session_max_age_hint',     type: 'number', envVar: 'WDBGP_SESSION_MAX_AGE' },
      admin_cookie_secure: { label: 'settings.admin_cookie_secure', hint: 'settings.admin_cookie_secure_hint', type: 'select',
        options: { auto: 'settings.auto', true: 'settings.true', false: 'settings.false' }, envVar: 'WDBGP_ADMIN_COOKIE_SECURE' },
    },
  },
  {
    name: 'settings.section_network',
    fields: {
      host: { label: 'settings.host', hint: 'settings.host_hint', type: 'string', readonly: true, envVar: 'WDBGP_HOST' },
      port: { label: 'settings.port', hint: 'settings.port_hint', type: 'number', readonly: true, envVar: 'WDBGP_PORT' },
    },
  },
  {
    // BGP identity/wiring only — every field here requires a restart.
    // Peer connection behavior/policy lives in section_bgp_peers below.
    name: 'settings.section_bgp',
    fields: {
      local_asn:         { label: 'settings.local_asn',         hint: 'settings.local_asn_hint',         type: 'number', restart: true, envVar: 'WDBGP_LOCAL_ASN' },
      router_id:         { label: 'settings.router_id',         hint: 'settings.router_id_hint',         type: 'string', restart: true, envVar: 'WDBGP_ROUTER_ID' },
      bgp_port:          { label: 'settings.bgp_port',          hint: 'settings.bgp_port_hint',          type: 'number', restart: true, envVar: 'WDBGP_BGP_PORT' },
      local_address_v4:  { label: 'settings.local_address_v4',  hint: 'settings.local_address_v4_hint',  type: 'string', restart: true, envVar: 'WDBGP_BGP_LOCAL_ADDRESS' },
      local_address_v6:  { label: 'settings.local_address_v6',  hint: 'settings.local_address_v6_hint',  type: 'string', restart: true, envVar: 'WDBGP_BGP_LOCAL_ADDRESS_V6' },
    },
  },
  {
    name: 'settings.section_bgp_peers',
    fields: {
      active_dial:                    { label: 'settings.active_dial',                      hint: 'settings.active_dial_hint',                      type: 'bool', restart: true, envVar: 'WDBGP_ACTIVE_DIAL' },
      allow_dynamic_peers:            { label: 'settings.allow_dynamic_peers',              hint: 'settings.allow_dynamic_peers_hint',              type: 'bool', envVar: 'WDBGP_ALLOW_DYNAMIC_PEERS' },
      dynamic_peer_md5_match:         { label: 'settings.dynamic_peer_md5_match',           hint: 'settings.dynamic_peer_md5_match_hint',           type: 'bool', restart: true, envVar: 'WDBGP_DYNAMIC_PEER_MD5_MATCH' },
      dynamic_peer_md5_queue_num:     { label: 'settings.dynamic_peer_md5_queue_num',       hint: 'settings.dynamic_peer_md5_queue_num_hint',       type: 'number', restart: true, envVar: 'WDBGP_DYNAMIC_PEER_MD5_QUEUE_NUM' },
      require_password_for_non_unique_ip: { label: 'settings.require_password_for_non_unique_ip', hint: 'settings.require_password_for_non_unique_ip_hint', type: 'bool', envVar: 'WDBGP_REQUIRE_PASSWORD_FOR_NON_UNIQUE_IP' },
    },
  },
  {
    name: 'settings.section_security',
    fields: {
      security_headers:    { label: 'settings.security_headers',    hint: 'settings.security_headers_hint',    type: 'bool', envVar: 'WDBGP_SECURITY_HEADERS' },
      trust_proxy_headers: { label: 'settings.trust_proxy_headers', hint: 'settings.trust_proxy_headers_hint', type: 'bool', envVar: 'WDBGP_TRUST_PROXY_HEADERS' },
      default_web_auth:    { label: 'settings.default_web_auth',    hint: 'settings.default_web_auth_hint',    type: 'select',
        options: { network: 'users.web_auth_network', login: 'users.web_auth_login', both: 'users.web_auth_both', any: 'users.web_auth_any' }, envVar: 'WDBGP_DEFAULT_WEB_AUTH' },
    },
  },
  {
    name: 'settings.section_localization',
    fields: {
      default_language: { label: 'settings.default_language', hint: 'settings.default_language_hint', type: 'select',
        options: { en: 'language.english', ru: 'language.russian' }, envVar: 'WDBGP_DEFAULT_LANGUAGE' },
    },
  },
  {
    name: 'settings.section_filters',
    fields: {
      filter_allow: { label: 'settings.filter_allow', hint: 'settings.filter_allow_hint', type: 'textarea' },
      filter_deny:  { label: 'settings.filter_deny',  hint: 'settings.filter_deny_hint',  type: 'textarea' },
    },
  },
  {
    name: 'settings.section_sync',
    fields: {
      sync_interval: { label: 'settings.sync_interval', hint: 'settings.sync_interval_hint', type: 'number', envVar: 'WDBGP_SYNC_INTERVAL' },
    },
  },
  {
    name: 'settings.section_js',
    fields: {
      js_timeout:       { label: 'settings.js_timeout',       hint: 'settings.js_timeout_hint',       type: 'number', envVar: 'WDBGP_JS_TIMEOUT' },
      js_max_source:    { label: 'settings.js_max_source',    hint: 'settings.js_max_source_hint',    type: 'number', envVar: 'WDBGP_JS_MAX_SOURCE' },
      js_max_response:  { label: 'settings.js_max_response',  hint: 'settings.js_max_response_hint',  type: 'number', envVar: 'WDBGP_JS_MAX_RESPONSE' },
      js_max_total:     { label: 'settings.js_max_total',     hint: 'settings.js_max_total_hint',     type: 'number', envVar: 'WDBGP_JS_MAX_TOTAL' },
      js_max_entries:   { label: 'settings.js_max_entries',   hint: 'settings.js_max_entries_hint',   type: 'number', envVar: 'WDBGP_JS_MAX_ENTRIES' },
      js_max_requests:  { label: 'settings.js_max_requests',  hint: 'settings.js_max_requests_hint',  type: 'number', envVar: 'WDBGP_JS_MAX_REQUESTS' },
      js_max_call_stack:{ label: 'settings.js_max_call_stack', hint: 'settings.js_max_call_stack_hint', type: 'number', envVar: 'WDBGP_JS_MAX_CALL_STACK' },
    },
  },
  {
    name: 'settings.section_rate_limit',
    fields: {
      rate_limit_login: { label: 'settings.rate_limit_login', hint: 'settings.rate_limit_login_hint', type: 'number', envVar: 'WDBGP_RATE_LIMIT_LOGIN' },
      rate_limit_admin: { label: 'settings.rate_limit_admin', hint: 'settings.rate_limit_admin_hint', type: 'number', envVar: 'WDBGP_RATE_LIMIT_ADMIN' },
    },
  },
  {
    name: 'settings.section_metrics',
    fields: {
      metrics_enabled:     { label: 'settings.metrics_enabled',     hint: 'settings.metrics_enabled_hint',     type: 'bool' },
      metrics_history_days:{ label: 'settings.metrics_history_days', hint: 'settings.metrics_history_days_hint', type: 'number' },
    },
  },
  {
    // The database file and its own backup/restore — one subsystem.
    // Feed-adapter source backups are a different subsystem, see
    // section_adapter_backup below.
    name: 'settings.section_database',
    fields: {
      db_path:              { label: 'settings.db_path',              hint: 'settings.db_path_hint',              type: 'string', readonly: true, envVar: 'WDBGP_DB' },
      backup_enabled:       { label: 'settings.backup_enabled',        hint: 'settings.backup_enabled_hint',        type: 'bool', readonly: true, envVar: 'WDBGP_BACKUP_ENABLED' },
      backup_dir:           { label: 'settings.backup_dir',            hint: 'settings.backup_dir_hint',            type: 'string', readonly: true, envVar: 'WDBGP_BACKUP_DIR' },
      auto_restore_enabled: { label: 'settings.auto_restore_enabled',  hint: 'settings.auto_restore_enabled_hint',  type: 'bool', readonly: true, envVar: 'WDBGP_AUTO_RESTORE_ENABLED' },
    },
  },
  {
    name: 'settings.section_adapter_backup',
    fields: {
      adapter_backup_dir: { label: 'settings.adapter_backup_dir', hint: 'settings.adapter_backup_dir_hint', type: 'string', envVar: 'WDBGP_ADAPTER_BACKUP_DIR' },
      adapter_backup_max: { label: 'settings.adapter_backup_max', hint: 'settings.adapter_backup_max_hint', type: 'number', envVar: 'WDBGP_ADAPTER_BACKUP_MAX' },
    },
  },
  {
    name: 'settings.section_status_api',
    fields: {
      status_allowed: { label: 'settings.status_allowed', hint: 'settings.status_allowed_hint', type: 'string', envVar: 'WDBGP_STATUS_ALLOWED' },
      status_token:   { label: 'settings.status_token',   hint: 'settings.status_token_hint',   type: 'string', envVar: 'WDBGP_STATUS_TOKEN' },
    },
  },
  {
    name: 'settings.section_logging',
    fields: {
      log_level:  { label: 'settings.log_level',  hint: 'settings.log_level_hint',  type: 'select',
        options: { DEBUG: 'DEBUG', INFO: 'INFO', WARN: 'WARN', ERROR: 'ERROR', FATAL: 'FATAL', PANIC: 'PANIC' }, envVar: 'WDBGP_LOG_LEVEL' },
      log_format: { label: 'settings.log_format', hint: 'settings.log_format_hint', type: 'select',
        options: { text: 'text', json: 'json' }, envVar: 'WDBGP_LOG_FORMAT' },
    },
  },
]
