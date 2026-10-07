import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'
import ConfigPage from '../ConfigPage.vue'
import type { ConfigSnapshot, ConfigDiff, ConfigUser, ConfigMode } from '@/types/config-snapshot'

const { mockGet, mockPost } = vi.hoisted(() => ({ mockGet: vi.fn(), mockPost: vi.fn() }))

vi.mock('@/api/client', () => ({
  default: {
    get: mockGet,
    post: mockPost,
    interceptors: { response: { use: vi.fn() } },
  },
}))

const mockToastAdd = vi.fn()
vi.mock('primevue/usetoast', () => ({
  useToast: () => ({ add: mockToastAdd }),
}))

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      'config.import_no_changes': 'No differences — already up to date',
      'config.import_invalid_file': "That file isn't a valid configuration export",
      'config.import_stale': 'This file changed, or was already applied, since the preview — upload it again',
      'config.diff.no_changes': 'No differences',
      'config.diff.modes': 'Modes',
      'config.diff.users': 'Users',
      'config.diff.added': 'Added ({count})',
      'config.diff.changed': 'Changed ({count})',
      'config.diff.removed': 'Not in the file — left as is ({count})',
    },
  },
})

function emptySnapshot(overrides: Partial<ConfigSnapshot> = {}): ConfigSnapshot {
  return {
    schema_version: 1,
    generated_at: 0,
    global_filters: { allow: [], deny: [] },
    modes: [],
    users: [],
    ...overrides,
  }
}

function cfgUser(name: string): ConfigUser {
  return {
    name, peer_ip: '', peer_asn: 0, networks: [], enabled: true, selection_locked: false,
    filter_mode: 'global', filter_editable: false, catalog_mode: 'OpenCCK', catalog_mode_editable: false,
    active_dial: false, web_auth: 'network', has_bgp_password: false,
    route_filters: { allow: [], deny: [] }, selected_categories: [], selected_services: [],
  }
}

function cfgMode(name: string): ConfigMode {
  return { name, enabled: true, feeds: [], communities: [] }
}

function emptyDiff(overrides: Partial<ConfigDiff> = {}): ConfigDiff {
  return {
    global_filters_changed: false,
    global_filters_before: { allow: [], deny: [] },
    global_filters_after: { allow: [], deny: [] },
    modes: { added: [], removed: [], changed: [] },
    users: { added: [], removed: [], changed: [] },
    ...overrides,
  }
}

function mountPage() {
  return mount(ConfigPage, {
    global: {
      plugins: [i18n, PrimeVue],
      stubs: {
        Button: { props: ['label', 'loading', 'disabled'], template: '<button class="stub-btn" :disabled="disabled">{{ label }}</button>' },
        Dialog: { props: ['visible'], template: '<div v-if="visible"><slot /><slot name="footer" /></div>' },
      },
    },
  })
}

async function selectFile(wrapper: ReturnType<typeof mountPage>, testid: string, content: unknown) {
  const input = wrapper.find(`[data-testid="${testid}"]`)
  const file = new File([JSON.stringify(content)], 'config.json', { type: 'application/json' })
  Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
  await input.trigger('change')
  // happy-dom's FileReader completes over several ticks, not just one.
  for (let i = 0; i < 5; i++) {
    await new Promise((r) => setTimeout(r, 10))
    await wrapper.vm.$nextTick()
  }
}

describe('ConfigPage', () => {
  beforeEach(() => {
    mockGet.mockReset()
    mockPost.mockReset()
    mockToastAdd.mockReset()
  })

  it('exports the current configuration', async () => {
    mockGet.mockResolvedValue({ data: emptySnapshot() })
    const wrapper = mountPage()
    await wrapper.find('[data-testid="config-export"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    expect(mockGet).toHaveBeenCalledWith('/admin/config/export')
  })

  it('previews an uploaded file and opens the confirm dialog when it differs', async () => {
    const diff = emptyDiff({ users: { added: [cfgUser('carol')], removed: [], changed: [] } })
    mockPost.mockResolvedValue({ data: { diff, digest: 'abc123' } })
    const wrapper = mountPage()

    await selectFile(wrapper, 'config-import-file', emptySnapshot({ users: [cfgUser('carol')] }))

    expect(mockPost).toHaveBeenCalledWith('/admin/config/import/preview', { snapshot: expect.any(Object) })
    expect(wrapper.text()).toContain('Added (1)')
    expect(wrapper.text()).toContain('carol')
  })

  it('shows a no-changes toast and never opens the dialog when the file matches exactly', async () => {
    mockPost.mockResolvedValue({ data: { diff: emptyDiff(), digest: 'abc123' } })
    const wrapper = mountPage()

    await selectFile(wrapper, 'config-import-file', emptySnapshot())

    expect(mockToastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'info' }))
    expect(wrapper.find('[data-testid="config-import-apply"]').exists()).toBe(false)
  })

  it('rejects a file that is not valid JSON', async () => {
    const wrapper = mountPage()
    const input = wrapper.find('[data-testid="config-import-file"]')
    const file = new File(['not json'], 'config.json', { type: 'application/json' })
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')
    for (let i = 0; i < 5; i++) {
      await new Promise((r) => setTimeout(r, 10))
      await wrapper.vm.$nextTick()
    }

    expect(mockPost).not.toHaveBeenCalled()
    expect(mockToastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'error' }))
  })

  it('applies the import with the previewed digest and reports the result', async () => {
    const diff = emptyDiff({ users: { added: [cfgUser('carol')], removed: [], changed: [] } })
    mockPost.mockResolvedValueOnce({ data: { diff, digest: 'the-digest' } })
    const wrapper = mountPage()
    await selectFile(wrapper, 'config-import-file', emptySnapshot({ users: [cfgUser('carol')] }))

    mockPost.mockResolvedValueOnce({
      data: { result: { users_created: ['carol'], users_updated: null, modes_created: null, modes_updated: null, unknown_feeds: null, unknown_modes: null }, global_filters_applied: false },
    })
    await wrapper.find('[data-testid="config-import-apply"]').trigger('click')
    for (let i = 0; i < 5; i++) {
      await new Promise((r) => setTimeout(r, 10))
      await wrapper.vm.$nextTick()
    }

    expect(mockPost).toHaveBeenLastCalledWith('/admin/config/import', { snapshot: expect.any(Object), digest: 'the-digest' })
    expect(mockToastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'success' }))
  })

  it('warns and closes the dialog when the apply rejects a stale digest', async () => {
    const diff = emptyDiff({ users: { added: [cfgUser('carol')], removed: [], changed: [] } })
    mockPost.mockResolvedValueOnce({ data: { diff, digest: 'the-digest' } })
    const wrapper = mountPage()
    await selectFile(wrapper, 'config-import-file', emptySnapshot({ users: [cfgUser('carol')] }))

    const staleError = Object.assign(new Error('conflict'), { isAxiosError: true, response: { status: 409, data: {} } })
    mockPost.mockRejectedValueOnce(staleError)
    await wrapper.find('[data-testid="config-import-apply"]').trigger('click')
    for (let i = 0; i < 5; i++) {
      await new Promise((r) => setTimeout(r, 10))
      await wrapper.vm.$nextTick()
    }

    expect(mockToastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'warn' }))
    expect(wrapper.find('[data-testid="config-import-apply"]').exists()).toBe(false)
  })

  it('compares two uploaded files without applying anything', async () => {
    const diff = emptyDiff({ modes: { added: [cfgMode('Lab')], removed: [], changed: [] } })
    const wrapper = mountPage()

    await selectFile(wrapper, 'config-compare-a-file', emptySnapshot())
    await selectFile(wrapper, 'config-compare-b-file', emptySnapshot({ modes: [cfgMode('Lab')] }))

    mockPost.mockResolvedValue({ data: { diff } })
    await wrapper.find('[data-testid="config-compare-run"]').trigger('click')
    for (let i = 0; i < 5; i++) {
      await new Promise((r) => setTimeout(r, 10))
      await wrapper.vm.$nextTick()
    }

    expect(mockPost).toHaveBeenCalledWith('/admin/config/diff', { a: expect.any(Object), b: expect.any(Object) })
    expect(wrapper.text()).toContain('Lab')
  })
})
