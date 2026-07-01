import { describe, it, expect } from 'vitest'
import {
  settingsSchema,
  settingsResponseSchema,
  settingBoolSchema,
  settingIntSchema,
  settingStringSchema,
} from '../settings'
import type { SettingBool, SettingInt, SettingString } from '../settings'

describe('settingBoolSchema', () => {
  it('parses a valid bool setting', () => {
    const result = settingBoolSchema.parse({ value: null, default_value: false, env_override: false })
    expect(result.value).toBeNull()
    expect(result.default_value).toBe(false)
  })

  it('parses a bool setting with a value', () => {
    const result = settingBoolSchema.parse({ value: true, default_value: false, env_override: true })
    expect(result.value).toBe(true)
    expect(result.env_override).toBe(true)
  })

  it('rejects invalid bool value type', () => {
    expect(() => settingBoolSchema.parse({ value: 'true', default_value: false, env_override: false }))
      .toThrow()
  })

  it('rejects missing default_value', () => {
    expect(() => settingBoolSchema.parse({ value: null, env_override: false }))
      .toThrow()
  })
})

describe('settingIntSchema', () => {
  it('parses a valid int setting', () => {
    const result = settingIntSchema.parse({ value: null, default_value: 8080, env_override: false })
    expect(result.value).toBeNull()
    expect(result.default_value).toBe(8080)
  })

  it('parses an int setting with a value', () => {
    const result = settingIntSchema.parse({ value: 179, default_value: 8080, env_override: false })
    expect(result.value).toBe(179)
  })

  it('rejects float for int field', () => {
    expect(() => settingIntSchema.parse({ value: 3.14, default_value: 0, env_override: false }))
      .toThrow()
  })

  it('rejects string for int field', () => {
    expect(() => settingIntSchema.parse({ value: '8080', default_value: 0, env_override: false }))
      .toThrow()
  })
})

describe('settingStringSchema', () => {
  it('parses a valid string setting', () => {
    const result = settingStringSchema.parse({ value: null, default_value: '0.0.0.0', env_override: false })
    expect(result.value).toBeNull()
    expect(result.default_value).toBe('0.0.0.0')
  })

  it('parses a string setting with a value', () => {
    const result = settingStringSchema.parse({ value: '127.0.0.1', default_value: '0.0.0.0', env_override: false })
    expect(result.value).toBe('127.0.0.1')
  })

  it('rejects number for string field', () => {
    expect(() => settingStringSchema.parse({ value: 8080, default_value: '0.0.0.0', env_override: false }))
      .toThrow()
  })
})

describe('settingsSchema', () => {
  it('parses a partial settings object with required shape fields', () => {
    const partial = {
      port: { value: null, default_value: 8080, env_override: false },
      host: { value: '127.0.0.1', default_value: '0.0.0.0', env_override: false },
    }
    // Use partial() to make all fields optional for partial object parsing
    const result = settingsSchema.partial().parse(partial)
    expect(result.port.value).toBeNull()
    expect(result.host.value).toBe('127.0.0.1')
  })

  it('parses a complete settings object with all fields', () => {
    const mkBool = (v: boolean | null, dv: boolean, env: boolean) =>
      ({ value: v, default_value: dv, env_override: env })
    const mkInt = (v: number | null, dv: number, env: boolean) =>
      ({ value: v, default_value: dv, env_override: env })
    const mkStr = (v: string | null, dv: string, env: boolean) =>
      ({ value: v, default_value: dv, env_override: env })

    const all: Record<string, unknown> = {
      active_dial: mkBool(true, true, false),
      adapter_backup_dir: mkStr(null, '/data/backup/adapters', false),
      adapter_backup_max: mkInt(null, 10, false),
      admin_cookie_secure: mkStr('auto', 'auto', false),
      admin_password: mkStr(null, '', false),
      allow_dynamic_peers: mkBool(false, false, false),
      auto_restore_enabled: mkBool(false, false, false),
      bgp_port: mkInt(null, 179, false),
      backup_dir: mkStr(null, '/data', false),
      backup_enabled: mkBool(true, true, false),
      db_path: mkStr(null, '/data/wdbgp.sqlite3', false),
      default_language: mkStr('en', 'en', false),
      default_web_auth: mkStr('network', 'network', false),
      filter_allow: mkStr('', '', false),
      filter_deny: mkStr('', '', false),
      host: mkStr(null, '0.0.0.0', false),
      js_max_call_stack: mkInt(null, 1000, false),
      js_max_entries: mkInt(null, 1000000, false),
      js_max_requests: mkInt(null, 200, false),
      js_max_response: mkInt(null, 16777216, false),
      js_max_source: mkInt(null, 1048576, false),
      js_max_total: mkInt(null, 67108864, false),
      js_timeout: mkInt(null, 120, false),
      local_asn: mkInt(null, 64512, false),
      local_address_v4: mkStr(null, '192.0.2.2', false),
      local_address_v6: mkStr(null, '', false),
      log_format: mkStr('text', 'text', false),
      log_level: mkStr('INFO', 'INFO', false),
      metrics_enabled: mkBool(false, false, false),
      metrics_history_days: mkInt(null, 14, false),
      port: mkInt(null, 8080, false),
      rate_limit_admin: mkInt(null, 30, false),
      rate_limit_login: mkInt(null, 5, false),
      require_password_for_non_unique_ip: mkBool(true, true, false),
      router_id: mkStr(null, '192.0.2.1', false),
      security_headers: mkBool(false, false, false),
      session_max_age: mkInt(null, 28800, false),
      session_secret: mkStr(null, '', false),
      status_allowed: mkStr(null, '', false),
      status_token: mkStr(null, '', false),
      sync_interval: mkInt(null, 3600, false),
      trust_proxy_headers: mkBool(false, false, false),
    }

    const result = settingsSchema.parse(all)
    expect(result.port.default_value).toBe(8080)
    expect(result.metrics_enabled.value).toBe(false)
  })
})

describe('settingsResponseSchema', () => {
  it('parses flat settings response (partial settings)', () => {
    const response = {
      port: { value: null, default_value: 8080, env_override: false },
      metrics_enabled: { value: true, default_value: false, env_override: false },
      host: { value: '127.0.0.1', default_value: '0.0.0.0', env_override: false },
      filter_allow: { value: '', default_value: '', env_override: false },
      filter_deny: { value: '', default_value: '', env_override: false },
    }
    // Build a partial schema for testing partial responses
    const partialResponseSchema = settingsSchema.partial()
    const result = partialResponseSchema.parse(response)
    expect(result.port?.default_value).toBe(8080)
    expect(result.filter_allow?.value).toBe('')
  })

  it('validates a complete flat settings response', () => {
    const mkBool = (v: boolean | null, dv: boolean, env: boolean) =>
      ({ value: v, default_value: dv, env_override: env })
    const mkInt = (v: number | null, dv: number, env: boolean) =>
      ({ value: v, default_value: dv, env_override: env })
    const mkStr = (v: string | null, dv: string, env: boolean) =>
      ({ value: v, default_value: dv, env_override: env })

    const response = {
      active_dial: mkBool(true, true, false),
      adapter_backup_dir: mkStr(null, '/data/backup/adapters', false),
      adapter_backup_max: mkInt(null, 10, false),
      admin_cookie_secure: mkStr('auto', 'auto', false),
      admin_password: mkStr(null, '', false),
      allow_dynamic_peers: mkBool(false, false, false),
      auto_restore_enabled: mkBool(false, false, false),
      bgp_port: mkInt(null, 179, false),
      backup_dir: mkStr(null, '/data', false),
      backup_enabled: mkBool(true, true, false),
      db_path: mkStr(null, '/data/wdbgp.sqlite3', false),
      default_language: mkStr('en', 'en', false),
      default_web_auth: mkStr('network', 'network', false),
      filter_allow: mkStr('', '', false),
      filter_deny: mkStr('', '', false),
      host: mkStr(null, '0.0.0.0', false),
      js_max_call_stack: mkInt(null, 1000, false),
      js_max_entries: mkInt(null, 1000000, false),
      js_max_requests: mkInt(null, 200, false),
      js_max_response: mkInt(null, 16777216, false),
      js_max_source: mkInt(null, 1048576, false),
      js_max_total: mkInt(null, 67108864, false),
      js_timeout: mkInt(null, 120, false),
      local_asn: mkInt(null, 64512, false),
      local_address_v4: mkStr(null, '192.0.2.2', false),
      local_address_v6: mkStr(null, '', false),
      log_format: mkStr('text', 'text', false),
      log_level: mkStr('INFO', 'INFO', false),
      metrics_enabled: mkBool(false, false, false),
      metrics_history_days: mkInt(null, 14, false),
      port: mkInt(null, 8080, false),
      rate_limit_admin: mkInt(null, 30, false),
      rate_limit_login: mkInt(null, 5, false),
      require_password_for_non_unique_ip: mkBool(true, true, false),
      router_id: mkStr(null, '192.0.2.1', false),
      security_headers: mkBool(false, false, false),
      session_max_age: mkInt(null, 28800, false),
      session_secret: mkStr(null, '', false),
      status_allowed: mkStr(null, '', false),
      status_token: mkStr(null, '', false),
      sync_interval: mkInt(null, 3600, false),
      trust_proxy_headers: mkBool(false, false, false),
    }

    const result = settingsResponseSchema.parse(response)
    expect(result.port.default_value).toBe(8080)
    expect(result.filter_allow.value).toBe('')
  })

  it('rejects response with unknown field', () => {
    expect(() => settingsResponseSchema.parse({
      port: { value: null, default_value: 8080, env_override: false },
      route_filters: { filter_allow: '', filter_deny: '' },
    })).toThrow()
  })
})

describe('type exports', () => {
  it('SettingBool type is compatible with schema', () => {
    const valid: SettingBool = { value: null, default_value: false, env_override: false }
    expect(valid.default_value).toBe(false)
  })

  it('SettingInt type is compatible with schema', () => {
    const valid: SettingInt = { value: null, default_value: 8080, env_override: false }
    expect(valid.default_value).toBe(8080)
  })

  it('SettingString type is compatible with schema', () => {
    const valid: SettingString = { value: null, default_value: 'hello', env_override: false }
    expect(valid.default_value).toBe('hello')
  })
})
