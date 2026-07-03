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
})
