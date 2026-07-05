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

  it('returns to the login screen when a count-prefixes fetch gets a 401 mid-session, instead of getting stuck on a dead authenticated view', async () => {
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
      modes: [],
    }
    mockGet.mockResolvedValue({ data: userData })
    // The session expires between the initial auth check and the
    // count-prefixes fetch that loadUserData triggers right after it.
    mockPost.mockRejectedValue({ isAxiosError: true, response: { status: 401 } })

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
    await wrapper.vm.$nextTick()

    // Only the login form's password input exists once authenticated flips
    // back to false — the main authenticated view (catalog, save button)
    // must not still be showing.
    expect(wrapper.find('input[type="password"]').exists()).toBe(true)
    expect(wrapper.find('select').exists()).toBe(false)
  })

  it('returns to the login screen when saveSelections gets a 401, instead of just showing a generic error toast', async () => {
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
      catalog: { CategoryA: ['svc1'] },
      selections: { categories: [], services: [] },
      communities: {},
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockGet.mockResolvedValue({ data: userData })
    mockPost.mockImplementation((url: string) => {
      if (url === '/user/selections') {
        return Promise.reject({ isAxiosError: true, response: { status: 401 } })
      }
      return Promise.resolve({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })
    })

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
    await wrapper.vm.$nextTick()

    const saveButton = wrapper.find('[data-testid="save-selections"]')
    expect(saveButton.exists()).toBe(true)
    await saveButton.trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('input[type="password"]').exists()).toBe(true)
  })

  it('refreshes counts after a successful save, so the delta badge does not keep showing the pre-save delta as the new baseline', async () => {
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
      catalog: { CategoryA: ['svc1'] },
      selections: { categories: [], services: [] },
      communities: {},
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockGet.mockResolvedValue({ data: userData })
    mockPost.mockImplementation((url: string) => {
      if (url === '/user/count-prefixes') {
        return Promise.resolve({ data: { v4: 100, v6: 50, delta_v4: 20, delta_v6: 10 } })
      }
      if (url === '/user/selections') {
        return Promise.resolve({ data: { ok: true } })
      }
      return Promise.resolve({ data: {} })
    })

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
    await wrapper.vm.$nextTick()

    const countPrefixesCallsBefore = mockPost.mock.calls.filter(([url]) => url === '/user/count-prefixes').length
    expect(countPrefixesCallsBefore).toBe(1) // the initial fetch from loadUserData

    const saveButton = wrapper.find('[data-testid="save-selections"]')
    await saveButton.trigger('click')
    await new Promise((r) => setTimeout(r, 0))
    await wrapper.vm.$nextTick()

    const countPrefixesCallsAfter = mockPost.mock.calls.filter(([url]) => url === '/user/count-prefixes').length
    expect(countPrefixesCallsAfter).toBe(2) // must re-fetch after save so the delta reflects the new baseline
  })

  it('shows 0, not the full catalog total, when the live count-prefixes fetch fails', async () => {
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
      catalog: { CategoryA: ['svc1'] },
      selections: { categories: [], services: [] },
      communities: {},
      // The full catalog has 500 IPv4 prefixes available — but the user
      // hasn't selected any of it, and the live selection-aware count
      // (countData) failed to load. The summary must not show 500 as if
      // it reflected the user's own selection.
      prefix_counts: { v4: { CategoryA: { svc1: 500 } }, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockGet.mockResolvedValue({ data: userData })
    mockPost.mockRejectedValue(new Error('network error'))

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
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="total-v4"]').text()).toContain('0')
    expect(wrapper.find('[data-testid="total-v4"]').text()).not.toContain('500')
  })

  it('ignores a stale count-prefixes response that arrives after a newer one, instead of clobbering it', async () => {
    vi.useFakeTimers()
    try {
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
        catalog: { CategoryA: ['svc1'], CategoryB: ['svc2'] },
        selections: { categories: [], services: [] },
        communities: {},
        prefix_counts: { v4: {}, v6: {} },
        filters: { allow: [], deny: [] },
        modes: [],
      }
      mockGet.mockResolvedValue({ data: userData })

      let resolveStale: (v: unknown) => void = () => {}
      let resolveFresh: (v: unknown) => void = () => {}
      let countCalls = 0
      mockPost.mockImplementation((url: string) => {
        if (url !== '/user/count-prefixes') return Promise.resolve({ data: {} })
        countCalls++
        if (countCalls === 1) {
          // Initial fetch from loadUserData at mount — resolve immediately.
          return Promise.resolve({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })
        }
        if (countCalls === 2) {
          return new Promise((resolve) => { resolveStale = resolve })
        }
        return new Promise((resolve) => { resolveFresh = resolve })
      })

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
      await vi.runOnlyPendingTimersAsync()
      await wrapper.vm.$nextTick()

      const categorySpan = wrapper.findAll('span').find((s) => s.text() === 'CategoryA')
      const otherCategorySpan = wrapper.findAll('span').find((s) => s.text() === 'CategoryB')
      expect(categorySpan).toBeTruthy()
      expect(otherCategorySpan).toBeTruthy()

      // Two toggles spaced past the 300ms debounce window, so each fires its
      // own request rather than collapsing into one.
      await categorySpan!.trigger('click')
      await vi.advanceTimersByTimeAsync(300)
      await otherCategorySpan!.trigger('click')
      await vi.advanceTimersByTimeAsync(300)

      expect(countCalls).toBe(3) // initial + stale + fresh

      // Fresh (later-dispatched) response arrives first...
      resolveFresh({ data: { v4: 99, v6: 0, delta_v4: 0, delta_v6: 0 } })
      await vi.runOnlyPendingTimersAsync()
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="total-v4"]').text()).toContain('99')

      // ...then the stale (earlier-dispatched) one arrives late. It must not
      // overwrite the fresh count that's already showing.
      resolveStale({ data: { v4: 1, v6: 0, delta_v4: 0, delta_v6: 0 } })
      await vi.runOnlyPendingTimersAsync()
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="total-v4"]').text()).toContain('99')
    } finally {
      vi.useRealTimers()
    }
  })
})
