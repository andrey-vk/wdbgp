import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'
import { settingsSchema } from '@/types/settings'
import apiClient from '@/api/client'

// ---------------------------------------------------------------------------
// Build a valid settings response for ALL fields in settingsSchema
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
}

// ---------------------------------------------------------------------------
// Mock apiClient
// ---------------------------------------------------------------------------

vi.mock('@/api/client', () => ({
  default: {
    get: vi.fn().mockResolvedValue({
      data: buildSettings(),
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
const toastAdd = vi.fn()
vi.mock('primevue/usetoast', () => ({
  useToast: () => ({ add: toastAdd }),
}))

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      'menu.settings': 'Settings',
      'settings.section_admin_access': 'Admin Access',
      'settings.section_network': 'Network',
      'settings.section_localization': 'Localization',
      'settings.section_security': 'Security',
      'settings.section_bgp': 'BGP',
      'settings.section_filters': 'Route Filters',
      'settings.section_sync': 'Data Sync',
      'settings.section_js': 'JavaScript Limits',
      'settings.section_rate_limit': 'Rate Limiting',
      'settings.section_metrics': 'Metrics',
      'settings.section_database': 'Database',
      'settings.section_status_api': 'Status API',
      'settings.section_logging': 'Logging',
      'settings.section_backup': 'Backup',
      'settings.purge_metrics': 'Purge Metrics',
      'settings.purge_metrics_hint': 'Delete all metrics history',
      'settings.purge_metrics_button': 'Purge',
      'settings.save': 'Save',
      'settings.saved': 'Saved',
      'settings.click_to_override': 'Click to override',
      'settings.revert_default': 'Revert to default',
      'settings.env_override_hint': 'Set via environment variable',
      'settings.requires_restart': 'Requires restart',
      'settings.empty': 'empty',
      'settings.on': 'On',
      'settings.off': 'Off',
      'settings.unsaved_confirm': 'Unsaved changes will be lost.',
      'settings.use_default': 'Use default',
      'settings.save_error': 'Failed to save settings.',
    },
  },
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
    expect(wrapper.text()).toContain('Save')
  })

  it('validates the settings response schema', () => {
    const response = buildSettings()

    const partialResponseSchema = settingsSchema.partial()

    // Should not throw
    const parsed = partialResponseSchema.parse(response)
    expect(parsed.filter_allow?.value).toBe('')
    expect(parsed.port?.default_value).toBe(8080)
  })

  it('sends typed values in PUT body', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    // Directly set values and trigger save. default_language is set to
    // 'ru' (not the mocked loaded value 'en') so it counts as a real
    // change under the unchanged-field filter in handleSave.
    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    vm.values = {
      bgp_port: 9090,
      metrics_enabled: true,
      default_language: 'ru',
      filter_allow: '',
      filter_deny: '',
    }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockClear()
    await vm.handleSave()

    // Verify typed values were sent
    const body = putMock.mock.calls[0][1] as Record<string, unknown>
    expect(body.bgp_port).toBe(9090)
    expect(typeof body.bgp_port).toBe('number')
    expect(body.metrics_enabled).toBe(true)
    expect(typeof body.metrics_enabled).toBe('boolean')
    expect(body.default_language).toBe('ru')
    expect(typeof body.default_language).toBe('string')
  })

  it('skips env-overridden fields in PUT body', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    vm.values = {
      port: 9090,
      bgp_port: 179,
    }
    vm.envOverrides = {
      port: true,
      bgp_port: false,
    }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockClear()
    await vm.handleSave()

    const body = putMock.mock.calls[0][1] as Record<string, unknown>
    // port is env-overridden and should be skipped
    expect(body).not.toHaveProperty('port')
    // bgp_port is not overridden and should be present
    expect(body.bgp_port).toBe(179)
  })

  it('skips readonly fields in PUT body even when not env-overridden', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    // host/port/db_path are readonly (env-only) but not currently env-overridden,
    // e.g. a deployment relying on hardcoded defaults instead of setting the env vars.
    vm.values = {
      host: '0.0.0.0',
      port: 8080,
      db_path: '/data/wdbgp.sqlite3',
      metrics_enabled: true,
    }
    vm.envOverrides = {
      host: false,
      port: false,
      db_path: false,
    }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockClear()
    await vm.handleSave()

    const body = putMock.mock.calls[0][1] as Record<string, unknown>
    expect(body).not.toHaveProperty('host')
    expect(body).not.toHaveProperty('port')
    expect(body).not.toHaveProperty('db_path')
    expect(body.metrics_enabled).toBe(true)
  })

  it('skips values with no known settingsMeta entry (whitelist, not blacklist)', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    // A key present in values (e.g. a field the backend started returning
    // that settingsMeta.ts was never updated for) has no metaMap entry at
    // all — not even readonly:false. It must never be forwarded, since the
    // frontend has no way to know whether the backend can accept a write
    // for it.
    vm.values = {
      totally_unrecognized_field: 'x',
      bgp_port: 179,
    }
    vm.envOverrides = {}

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockClear()
    await vm.handleSave()

    const body = putMock.mock.calls[0][1] as Record<string, unknown>
    expect(body).not.toHaveProperty('totally_unrecognized_field')
    expect(body.bgp_port).toBe(179)
  })

  it('stops saving spinner on error', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    vm.values = { port: 9090 }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockRejectedValueOnce(new Error('network error'))
    await vm.handleSave()

    // Saving must be false even after error
    expect(vm.saving).toBe(false)
    // Dirty should still be true (save failed)
    expect(vm.dirty).toBe(true)
  })

  it('skips password fields with empty value on save', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    // Set non-password field + empty password + filled password
    vm.values = {
      bgp_port: 9090,
      admin_password: '',        // empty → should be skipped
      session_secret: 'new-secret', // non-empty → should be sent
    }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockClear()
    await vm.handleSave()

    const body = putMock.mock.calls[0][1] as Record<string, unknown>
    // bgp_port should be present
    expect(body.bgp_port).toBe(9090)
    // admin_password (empty) should be skipped
    expect(body).not.toHaveProperty('admin_password')
    // session_secret (non-empty) should be included
    expect(body.session_secret).toBe('new-secret')
  })

  it('skips password fields with null value on save', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    // null password fields (default state from backend) should be skipped
    vm.values = {
      bgp_port: 8080,
      admin_password: null,
    }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockClear()
    await vm.handleSave()

    const body = putMock.mock.calls[0][1] as Record<string, unknown>
    // bgp_port should be present
    expect(body.bgp_port).toBe(8080)
    // admin_password (null) should be skipped
    expect(body).not.toHaveProperty('admin_password')
  })

  it('excludes fields whose value has not changed since load from the PUT body', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    // active_dial was loaded as `true` (buildSettings) and is left exactly
    // as-is here — only bgp_port is actually edited. A save that only
    // touches bgp_port must not also re-fire active_dial's OnChange (which
    // could spuriously mark a BGP restart as pending for a field the admin
    // never touched).
    vm.values = {
      ...vm.values,
      active_dial: true,
      bgp_port: 9090,
    }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    putMock.mockClear()
    await vm.handleSave()

    const body = putMock.mock.calls[0][1] as Record<string, unknown>
    expect(body).not.toHaveProperty('active_dial')
    expect(body.bgp_port).toBe(9090)
  })

  it('shows a warning toast (not the success banner) when the backend reports a partial-apply warning', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    vm.values = { filter_allow: '10.0.0.0/8' }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    const getMock = apiClient.get as ReturnType<typeof vi.fn>
    putMock.mockClear()
    getMock.mockClear()
    toastAdd.mockClear()
    putMock.mockResolvedValueOnce({
      data: { ok: true, warning: 'Settings saved, but BGP reconciliation failed: bgp speaker is not running' },
    })

    await vm.handleSave()

    // apiSettingsPut already persisted the change before Reconcile failed —
    // the PUT is not retried, but the form must not claim full success, and
    // must re-fetch the authoritative state rather than trust local edits.
    expect(vm.saved).toBe(false)
    expect(toastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'warn' }))
    expect(getMock).toHaveBeenCalled()
    expect(vm.dirty).toBe(false)
  })

  it('re-fetches settings after a save error instead of trusting local edits', async () => {
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
          Button: { props: ['label', 'loading', 'severity'], template: '<button>{{ label }}</button>' },
          Message: { props: ['severity'], template: '<div class="stub-message"><slot /></div>' },
          Tag: { template: '<span class="stub-tag"><slot /></span>' },
        },
      },
    })
    await wrapper.vm.$nextTick()
    await new Promise(r => setTimeout(r, 50))

    const vm = wrapper.vm as InstanceType<typeof SettingsPage>
    vm.values = { bgp_port: 9999 }

    const putMock = apiClient.put as ReturnType<typeof vi.fn>
    const getMock = apiClient.get as ReturnType<typeof vi.fn>
    getMock.mockClear()
    putMock.mockRejectedValueOnce(new Error('network error'))

    await vm.handleSave()

    // apiSettingsPut validates every key before applying any of them, but a
    // later key can still fail mid-apply after an earlier one was already
    // persisted — the form must not assume "error means nothing changed".
    expect(getMock).toHaveBeenCalled()
  })
})
