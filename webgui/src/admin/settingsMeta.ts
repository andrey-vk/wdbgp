export interface SettingMeta {
  label: string
  hint: string
  type: 'bool' | 'number' | 'string' | 'select'
  restart?: boolean
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
  active_dial: 'true',
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

// Sections match allSettings() layout from internal/web/settings_helpers.go
export const sections: SettingsSection[] = [
  {
    name: 'settings.section_general',
    fields: {
      default_language: { label: 'settings.default_language', hint: 'settings.default_language_hint', type: 'select',
        options: { en: 'language.english', ru: 'language.russian' }, envVar: 'WDBGP_DEFAULT_LANGUAGE' },
      session_max_age:  { label: 'settings.session_max_age',  hint: 'settings.session_max_age_hint',  type: 'number', envVar: 'WDBGP_SESSION_MAX_AGE' },
      admin_cookie_secure: { label: 'settings.admin_cookie_secure', hint: 'settings.admin_cookie_secure_hint', type: 'select',
        options: { auto: 'settings.auto', true: 'settings.true', false: 'settings.false' }, envVar: 'WDBGP_ADMIN_COOKIE_SECURE' },
      trust_proxy_headers: { label: 'settings.trust_proxy_headers', hint: 'settings.trust_proxy_headers_hint', type: 'bool', envVar: 'WDBGP_TRUST_PROXY_HEADERS' },
      security_headers:    { label: 'settings.security_headers',    hint: 'settings.security_headers_hint',    type: 'bool', envVar: 'WDBGP_SECURITY_HEADERS' },
      default_web_auth:    { label: 'settings.default_web_auth',    hint: 'settings.default_web_auth_hint',    type: 'select',
        options: { network: 'users.web_auth_network', login: 'users.web_auth_login', both: 'users.web_auth_both', any: 'users.web_auth_any' }, envVar: 'WDBGP_DEFAULT_WEB_AUTH' },
      status_allowed:      { label: 'settings.status_allowed',      hint: 'settings.status_allowed_hint',      type: 'string', envVar: 'WDBGP_STATUS_ALLOWED' },
      status_token:        { label: 'settings.status_token',        hint: 'settings.status_token_hint',        type: 'string', envVar: 'WDBGP_STATUS_TOKEN' },
      adapter_backup_dir:  { label: 'settings.adapter_backup_dir',  hint: 'settings.adapter_backup_dir_hint',  type: 'string', envVar: 'WDBGP_ADAPTER_BACKUP_DIR' },
      adapter_backup_max:  { label: 'settings.adapter_backup_max',  hint: 'settings.adapter_backup_max_hint',  type: 'number', envVar: 'WDBGP_ADAPTER_BACKUP_MAX' },
      metrics_enabled:     { label: 'settings.metrics_enabled',     hint: 'settings.metrics_enabled_hint',     type: 'bool' },
      metrics_history_days:{ label: 'settings.metrics_history_days', hint: 'settings.metrics_history_days_hint', type: 'number' },
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
    name: 'settings.section_logging',
    fields: {
      log_level:  { label: 'settings.log_level',  hint: 'settings.log_level_hint',  type: 'select',
        options: { DEBUG: 'DEBUG', INFO: 'INFO', WARN: 'WARN', ERROR: 'ERROR', FATAL: 'FATAL', PANIC: 'PANIC' }, envVar: 'WDBGP_LOG_LEVEL' },
      log_format: { label: 'settings.log_format', hint: 'settings.log_format_hint', type: 'select',
        options: { text: 'text', json: 'json' }, envVar: 'WDBGP_LOG_FORMAT' },
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
    name: 'settings.section_bgp',
    fields: {
      bgp_port:         { label: 'settings.bgp_port',         hint: 'settings.bgp_port_hint',         type: 'number', restart: true, envVar: 'WDBGP_BGP_PORT' },
      local_asn:        { label: 'settings.local_asn',        hint: 'settings.local_asn_hint',        type: 'number', restart: true, envVar: 'WDBGP_LOCAL_ASN' },
      router_id:        { label: 'settings.router_id',        hint: 'settings.router_id_hint',        type: 'string', restart: true, envVar: 'WDBGP_ROUTER_ID' },
      local_address_v4: { label: 'settings.local_address_v4', hint: 'settings.local_address_v4_hint', type: 'string', restart: true, envVar: 'WDBGP_BGP_LOCAL_ADDRESS' },
      local_address_v6: { label: 'settings.local_address_v6', hint: 'settings.local_address_v6_hint', type: 'string', restart: true, envVar: 'WDBGP_BGP_LOCAL_ADDRESS_V6' },
    },
  },
  {
    name: 'settings.section_network',
    fields: {
      host: { label: 'settings.host', hint: 'settings.host_hint', type: 'string', restart: true, envVar: 'WDBGP_HOST' },
      port: { label: 'settings.port', hint: 'settings.port_hint', type: 'number', restart: true, envVar: 'WDBGP_PORT' },
    },
  },
]
