import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import PrimeVue from 'primevue/config'

const mockGet = vi.fn()
const mockPost = vi.fn()
const mockPut = vi.fn()

vi.mock('@/api/client', () => ({
  default: {
    get: mockGet,
    post: mockPost,
    put: mockPut,
    interceptors: { response: { use: vi.fn() } },
  },
}))

vi.mock('primevue/usetoast', () => ({
  useToast: () => ({ add: vi.fn() }),
}))

vi.mock('@/plugins/i18n', () => ({
  switchLocale: vi.fn(),
  getCurrentLocale: () => 'en',
}))

const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: {} } })

describe('UserPage', () => {
  beforeEach(() => {
    mockGet.mockReset()
    mockPost.mockReset()
    mockPut.mockReset()
    // happy-dom doesn't fully implement localStorage; stub it (same as theme.test.ts)
    const store = new Map<string, string>()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => { store.set(key, value) },
      removeItem: (key: string) => { store.delete(key) },
      clear: () => { store.clear() },
      get length() { return store.size },
      key: (index: number) => Array.from(store.keys())[index] ?? null,
    })
    setActivePinia(createPinia())
  })

  it('checks auth via the shared apiClient (so CSRF headers are attached)', async () => {
    mockGet.mockResolvedValue({ data: {} })

    const UserPage = (await import('../UserPage.vue')).default
    mount(UserPage, {
      global: {
        plugins: [i18n, PrimeVue],
        stubs: {
          LanguageSwitcher: { template: '<div class="stub-language-switcher" />' },
          Toast: { template: '<div class="stub-toast" />' },
        },
      },
    })
    await new Promise((r) => setTimeout(r, 0))

    expect(mockGet).toHaveBeenCalledWith('/user/me')
  })

  it('resyncs from the server after a failed mode switch, since the backend may have already committed the mode change', async () => {
    const userData = {
      user: {
        id: 1,
        name: 'Alice',
        catalog_mode_id: 1,
        catalog_mode_name: 'Mode A',
        selection_locked: false,
        filter_editable: false,
        filter_override: false,
        filter_mode: 'allow',
        catalog_editable: true,
        networks: [],
      },
      catalog: {},
      selections: { categories: [], services: [] },
      communities: {},
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [
        { id: 1, key: 'a', name: 'Mode A', enabled: true, feed_count: 0 },
        { id: 2, key: 'b', name: 'Mode B', enabled: true, feed_count: 0 },
      ],
    }
    mockGet.mockResolvedValue({ data: userData })
    // The PUT reports failure (e.g. the backend committed the mode change
    // but BGP reconciliation failed afterwards, returning a 500).
    mockPut.mockRejectedValueOnce(new Error('reconcile failed'))

    const UserPage = (await import('../UserPage.vue')).default
    const wrapper = mount(UserPage, {
      global: {
        plugins: [i18n, PrimeVue],
        stubs: {
          LanguageSwitcher: { template: '<div class="stub-language-switcher" />' },
          Toast: { template: '<div class="stub-toast" />' },
        },
      },
    })
    await new Promise((r) => setTimeout(r, 0))
    expect(mockGet).toHaveBeenCalledTimes(1)

    const select = wrapper.find('select')
    await select.setValue('2')
    await new Promise((r) => setTimeout(r, 0))

    // Even though the PUT failed, the UI must re-fetch the authoritative
    // server state rather than silently keep showing pre-switch data.
    expect(mockGet).toHaveBeenCalledTimes(2)
  })
})
