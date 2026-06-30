import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'
import { z } from 'zod'
import { settingsSchema } from '@/types/settings'

// ---------------------------------------------------------------------------
// Build a valid settings response for ALL 40+ fields in settingsSchema
// ---------------------------------------------------------------------------

function mkBool(v: boolean | null, dv: boolean, env: boolean) {
  return { value: v, default_value: dv, env_override: env }
}
function mkInt(v: number | null, dv: number, env: boolean) {
  return { value: v, default_value: dv, env_override: env }
}
function mkStr(v: string | null, dv: string, env: boolean) {
  return { value: v, default_value: dv, env_override: env }
}

function buildSettings(): Record<string, unknown> {
  return {
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
}

// ---------------------------------------------------------------------------
// Mock apiClient
// ---------------------------------------------------------------------------

vi.mock('@/api/client', () => ({
  default: {
    get: vi.fn().mockResolvedValue({
      data: {
        settings: buildSettings(),
        route_filters: { filter_allow: '', filter_deny: '' },
      },
    }),
    put: vi.fn().mockResolvedValue({ data: { ok: true } }),
    post: vi.fn().mockResolvedValue({ data: { ok: true } }),
  },
}))

// Mock vue-router
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: '1' }, query: {}, path: '/' }),
  useRouter: () => ({ push: vi.fn() }),
  onBeforeRouteLeave: vi.fn(),
}))

// Mock primevue useToast
vi.mock('primevue/usetoast', () => ({
  useToast: () => ({ add: vi.fn() }),
}))

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: { en: {} },
})

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('SettingsPage', () => {
  it('renders all sections', async () => {
    const SettingsPage = (await import('../SettingsPage.vue')).default
    const wrapper = mount(SettingsPage, {
      global: {
        plugins: [i18n, PrimeVue],
        stubs: {
          SettingField: {
            props: ['fieldKey', 'meta', 'value', 'defaultValue', 'envOverride'],
            template: '<div class="stub-settingfield">{{ meta.label }}</div>',
          },
          Textarea: { props: ['modelValue'], template: '<textarea></textarea>' },
          Button: { props: ['label'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))
    // Should have sections rendered (at least one .card)
    expect(wrapper.findAll('.card').length).toBeGreaterThan(0)
    // The save button should be present
    expect(wrapper.text()).toContain('settings.save')
  })

  it('validates the settings response schema', () => {
    const response = {
      settings: buildSettings(),
      route_filters: { filter_allow: '', filter_deny: '' },
    }

    const partialResponseSchema = z.object({
      settings: settingsSchema.partial(),
      route_filters: z.object({
        filter_allow: z.string(),
        filter_deny: z.string(),
      }),
    })

    // Should not throw
    const parsed = partialResponseSchema.parse(response)
    expect(parsed.route_filters.filter_allow).toBe('')
    expect(parsed.settings.port?.default_value).toBe(8080)
  })
})
