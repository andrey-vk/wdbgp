import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

// ============================================================
// Global mocks
// ============================================================

// Mock vue-i18n
const tMock = vi.fn((key: string, params?: Record<string, unknown>) => {
  if (params && typeof params === 'object') {
    return key.replace(/\{(\w+)\}/g, (_, k) => String(params[k] ?? '?'))
  }
  return key
})

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: tMock }),
}))

// Mock api client
const mockGet = vi.fn()
const mockPost = vi.fn()
const mockPut = vi.fn()
const mockDelete = vi.fn()

vi.mock('@/api/client', () => ({
  default: {
    get: mockGet,
    post: mockPost,
    put: mockPut,
    delete: mockDelete,
    interceptors: { response: { use: vi.fn() } },
  },
}))

// Mock vue-router
const mockPush = vi.fn()
vi.mock('vue-router', () => ({
  useRoute: () => ({
    params: { id: '1' },
    query: {},
    path: '/',
  }),
  useRouter: () => ({
    push: mockPush,
  }),
  onBeforeRouteLeave: vi.fn(),
}))

// Mock primevue useConfirm
vi.mock('primevue/useconfirm', () => ({
  useConfirm: () => ({
    require: vi.fn(),
  }),
}))

// Mock primevue useToast
vi.mock('primevue/usetoast', () => ({
  useToast: () => ({
    add: vi.fn(),
  }),
}))

// Mock format utility
vi.mock('@/utils/format', () => ({
  formatDateTime: () => '2024-01-01',
}))

// Mock chart.js
vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  default: { register: vi.fn() },
}))

// ============================================================
// Helper to mount pages with common stubs
// ============================================================

function stubPrimeVueComponents() {
  return {
    Button: { props: ['label'], template: '<button class="stub-btn">{{ label }}<slot /></button>' },
    InputText: { template: '<input class="stub-input" />', inheritAttrs: false },
    InputNumber: { template: '<input class="stub-inputnum" type="number" />', inheritAttrs: false },
    Textarea: { template: '<textarea class="stub-textarea"></textarea>', inheritAttrs: false },
    Select: { template: '<select class="stub-select"><slot /></select>', inheritAttrs: false },
    ToggleSwitch: { template: '<input class="stub-toggle" type="checkbox" />', inheritAttrs: false },
    Checkbox: { template: '<input class="stub-checkbox" type="checkbox" />', inheritAttrs: false },
    Tag: { template: '<span class="stub-tag"><slot /></span>', inheritAttrs: false },
    Message: { template: '<div class="stub-message"><slot /></div>', inheritAttrs: false },
    Chart: { template: '<div class="stub-chart"></div>', inheritAttrs: false },
    Popover: { template: '<div class="stub-popover"><slot /></div>', inheritAttrs: false },
    Toast: { template: '<div class="stub-toast"></div>', inheritAttrs: false },
    ConfirmDialog: { template: '<div class="stub-confirm"></div>', inheritAttrs: false },
    ProgressSpinner: { template: '<div class="stub-spinner"></div>', inheritAttrs: false },
    SelectButton: {
      template: '<div class="stub-selectbtn"><slot /></div>',
      inheritAttrs: false,
    },
  }
}

function defaultGetMock() {
  mockGet.mockImplementation((url: string) => {
    const defaults: Record<string, unknown> = {
      '/admin/dashboard': {
        data: {
          prefixes: 0, categories: 0, services: 0,
          bgp: { connected_peers: 0, total_peers: 0, peers: [] },
          feeds: { enabled: 0, total: 0, items: [] },
          users: { total: 0 },
          modes: [],
          uptime_seconds: 0,
          user_history: [],
          feed_history: [],
        },
      },
      '/admin/users': { data: { users: [] } },
      '/admin/feeds': { data: { feeds: [] } },
      '/admin/modes': { data: { modes: [] } },
      '/admin/adapters': { data: { adapters: [] } },
      '/admin/settings': { data: { default_language: { value: 'en', default_value: 'en', env_override: false }, default_web_auth: { value: 'network', default_value: 'network', env_override: false }, filter_allow: { value: '', default_value: '', env_override: false }, filter_deny: { value: '', default_value: '', env_override: false }, active_dial: { value: null, default_value: false, env_override: false } } },
      '/admin/modes/1/communities': { data: { communities: [] } },
      '/admin/modes/1': { data: { name: 'Test Mode' } },
    }
    const d = defaults[url] || { data: {} }
    return Promise.resolve(d)
  })
}

// ============================================================
// Smoke tests
// ============================================================

describe('Pages smoke tests', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    setActivePinia(createPinia())
    defaultGetMock()

    // Stub localStorage
    const store = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => { store.set(key, value) },
      removeItem: (key: string) => { store.delete(key) },
      clear: () => { store.clear() },
      get length() { return store.size },
      key: (index: number) => Array.from(store.keys())[index] ?? null,
    })

    // Stub window.confirm
    vi.stubGlobal('confirm', vi.fn(() => true))

    document.documentElement.classList.remove('app-dark')
  })

  it('Dashboard renders without throwing and shows a loading state or content', async () => {
    const Dashboard = (await import('@/admin/views/Dashboard.vue')).default
    const wrapper = mount(Dashboard, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    const html = wrapper.html()
    expect(html.length).toBeGreaterThan(0)
  })

  it('UsersPage renders without throwing', async () => {
    const UsersPage = (await import('@/admin/views/UsersPage.vue')).default
    const wrapper = mount(UsersPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.html().length).toBeGreaterThan(0)
  })

  it('ModesPage renders without throwing', async () => {
    const ModesPage = (await import('@/admin/views/ModesPage.vue')).default
    const wrapper = mount(ModesPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.html().length).toBeGreaterThan(0)
  })

  it('FeedsPage renders without throwing', async () => {
    const FeedsPage = (await import('@/admin/views/FeedsPage.vue')).default
    const wrapper = mount(FeedsPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.html().length).toBeGreaterThan(0)
  })

  it('AdaptersPage renders without throwing', async () => {
    const AdaptersPage = (await import('@/admin/views/AdaptersPage.vue')).default
    const wrapper = mount(AdaptersPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.html().length).toBeGreaterThan(0)
  })

  it('SettingsPage renders without throwing', async () => {
    const SettingsPage = (await import('@/admin/views/SettingsPage.vue')).default
    const wrapper = mount(SettingsPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.html().length).toBeGreaterThan(0)
  })

  it('DebugPage renders without throwing', async () => {
    const DebugPage = (await import('@/admin/views/DebugPage.vue')).default
    const wrapper = mount(DebugPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.html().length).toBeGreaterThan(0)
  })

  it('CommunitiesPage renders without throwing', async () => {
    const CommunitiesPage = (await import('@/admin/views/CommunitiesPage.vue')).default
    const wrapper = mount(CommunitiesPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })
    expect(wrapper.exists()).toBe(true)
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.html().length).toBeGreaterThan(0)
  })
})

// ============================================================
// Initial-load failure fallback (a non-401 error, e.g. a network
// blip or 500 — a 401 is handled separately by apiClient's redirect
// interceptor, see src/api/__tests__/client.spec.ts)
// ============================================================

describe('Pages show a reload fallback when the initial load fails', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    setActivePinia(createPinia())

    const store = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => { store.set(key, value) },
      removeItem: (key: string) => { store.delete(key) },
      clear: () => { store.clear() },
      get length() { return store.size },
      key: (index: number) => Array.from(store.keys())[index] ?? null,
    })
  })

  async function expectErrorFallback(componentPath: string, failingUrl: string) {
    mockGet.mockImplementation((url: string) => {
      if (url === failingUrl) return Promise.reject(new Error('network error'))
      return Promise.resolve({ data: {} })
    })
    const Component = (await import(componentPath)).default
    const wrapper = mount(Component, {
      global: { stubs: stubPrimeVueComponents() },
    })
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.text()).toContain('error.generic_title')
    expect(wrapper.find('.stub-btn').exists()).toBe(true) // ErrorPage's reload button
  }

  it('UsersPage', () => expectErrorFallback('@/admin/views/UsersPage.vue', '/admin/users'))
  it('FeedsPage', () => expectErrorFallback('@/admin/views/FeedsPage.vue', '/admin/feeds'))
  it('AdaptersPage', () => expectErrorFallback('@/admin/views/AdaptersPage.vue', '/admin/adapters'))
  it('ModesPage', () => expectErrorFallback('@/admin/views/ModesPage.vue', '/admin/modes'))
  it('DebugPage', () => expectErrorFallback('@/admin/views/DebugPage.vue', '/admin/modes'))

  // CommunitiesPage's initial fetch is /admin/modes/:id, not a fixed URL
  // matched by expectErrorFallback's exact-match mock, so it gets its own
  // assertion. It also must NOT show "no communities configured" (the
  // empty-communities state) on a load failure — that's the actual bug:
  // a failed load looked identical to a mode that legitimately has none.
  it('CommunitiesPage', async () => {
    mockGet.mockImplementation((url: string) => {
      if (url === '/admin/modes/1') return Promise.reject(new Error('network error'))
      return Promise.resolve({ data: {} })
    })
    const CommunitiesPage = (await import('@/admin/views/CommunitiesPage.vue')).default
    const wrapper = mount(CommunitiesPage, {
      global: { stubs: stubPrimeVueComponents() },
    })
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(wrapper.text()).toContain('error.generic_title')
    expect(wrapper.text()).not.toContain('communities.none')
  })
})
