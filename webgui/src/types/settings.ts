import { z } from 'zod'

// Reusable setting sub-schemas — matches backend SettingJSON[T]
export const settingBoolSchema = z.object({
  value: z.boolean().nullable(),
  default_value: z.boolean(),
  env_override: z.boolean(),
})

export const settingIntSchema = z.object({
  value: z.number().int().nullable(),
  default_value: z.number().int(),
  env_override: z.boolean(),
})

export const settingStringSchema = z.object({
  value: z.string().nullable(),
  default_value: z.string(),
  env_override: z.boolean(),
})

// Full settings schema — matches backend SettingsJSON exactly
export const settingsSchema = z.object({
  active_dial: settingBoolSchema,
  adapter_backup_dir: settingStringSchema,
  adapter_backup_max: settingIntSchema,
  admin_cookie_secure: settingStringSchema,
  admin_password: settingStringSchema,
  allow_dynamic_peers: settingBoolSchema,
  auto_restore_enabled: settingBoolSchema,
  bgp_port: settingIntSchema,
  backup_dir: settingStringSchema,
  backup_enabled: settingBoolSchema,
  db_path: settingStringSchema,
  default_language: settingStringSchema,
  default_web_auth: settingStringSchema,
  filter_allow: settingStringSchema,
  filter_deny: settingStringSchema,
  host: settingStringSchema,
  js_max_call_stack: settingIntSchema,
  js_max_entries: settingIntSchema,
  js_max_requests: settingIntSchema,
  js_max_response: settingIntSchema,
  js_max_source: settingIntSchema,
  js_max_total: settingIntSchema,
  js_timeout: settingIntSchema,
  local_asn: settingIntSchema,
  local_address_v4: settingStringSchema,
  local_address_v6: settingStringSchema,
  log_format: settingStringSchema,
  log_level: settingStringSchema,
  metrics_enabled: settingBoolSchema,
  metrics_history_days: settingIntSchema,
  port: settingIntSchema,
  rate_limit_admin: settingIntSchema,
  rate_limit_login: settingIntSchema,
  require_password_for_non_unique_ip: settingBoolSchema,
  router_id: settingStringSchema,
  security_headers: settingBoolSchema,
  session_max_age: settingIntSchema,
  session_secret: settingStringSchema,
  status_allowed: settingStringSchema,
  status_token: settingStringSchema,
  sync_interval: settingIntSchema,
  trust_proxy_headers: settingBoolSchema,
})

// Full API response type — matches GET /api/admin/settings response (flat SettingsJSON)
export const settingsResponseSchema = settingsSchema

export type SettingsResponse = z.infer<typeof settingsResponseSchema>
export type Settings = z.infer<typeof settingsSchema>
export type SettingBool = z.infer<typeof settingBoolSchema>
export type SettingInt = z.infer<typeof settingIntSchema>
export type SettingString = z.infer<typeof settingStringSchema>
