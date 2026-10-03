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

// Default empty filters-in-effect payload for GET /user/route-filters.
// Tests that care about its content use mockGet.mockImplementation directly.
const defaultRouteFiltersInfo = {
  mode: 'global',
  global: { allow: [], deny: [] },
  own: { allow: [], deny: [] },
  effective: { allow: [], deny: [] },
}

// Resolves GET /user/me|/user/login with userData and GET /user/route-filters
// with an empty default — a URL-aware stand-in for the old blanket
// mockGet.mockResolvedValue({ data: userData }), needed since loadUserData
// now also fetches /user/route-filters on every load.
function mockUserDataGet(userData: unknown): void {
  mockGet.mockImplementation((url: string) => {
    if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
    if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
    return Promise.resolve({ data: userData })
  })
}

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
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [
        { id: 1, name: 'Mode A', enabled: true, feed_count: 0 },
        { id: 2, name: 'Mode B', enabled: true, feed_count: 0 },
      ],
    }
    mockUserDataGet(userData)
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
    // One load = /user/me + /user/route-filters + /user/feed-changes.
    expect(mockGet).toHaveBeenCalledTimes(3)

    const select = wrapper.find('select')
    await select.setValue('2')
    await new Promise((r) => setTimeout(r, 0))

    // Even though the PUT failed, the UI must re-fetch the authoritative
    // server state rather than silently keep showing pre-switch data.
    expect(mockGet).toHaveBeenCalledTimes(6)
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
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockUserDataGet(userData)
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
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockUserDataGet(userData)
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
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockUserDataGet(userData)
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
      communities: [],
      // The full catalog has 500 IPv4 prefixes available — but the user
      // hasn't selected any of it, and the live selection-aware count
      // (countData) failed to load. The summary must not show 500 as if
      // it reflected the user's own selection.
      prefix_counts: { v4: { CategoryA: { svc1: 500 } }, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockUserDataGet(userData)
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
        communities: [],
        prefix_counts: { v4: {}, v6: {} },
        filters: { allow: [], deny: [] },
        modes: [],
      }
      mockUserDataGet(userData)

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

  it('tracks two services independently even when category+service strings collide', async () => {
    // (category "a", service "b::c") and (category "a::b", service "c")
    // joined with "::" to the same string under the old key scheme. Only
    // the first is pre-selected.
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
      catalog: { a: ['b::c'], 'a::b': ['c'] },
      selections: { categories: [], services: [{ category: 'a', service: 'b::c' }] },
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockUserDataGet(userData)
    mockPost.mockResolvedValue({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })

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

    const selectedLabel = wrapper.findAll('span').find((s) => s.text() === 'b::c')
    const collidingLabel = wrapper.findAll('span').find((s) => s.text() === 'c')
    expect(selectedLabel).toBeDefined()
    expect(collidingLabel).toBeDefined()

    const checkboxFor = (label: typeof selectedLabel) =>
      label!.element.parentElement!.querySelector('div')!

    expect(checkboxFor(selectedLabel).classList.contains('bg-blue-500')).toBe(true)
    // Must NOT appear checked just because the colliding string is.
    expect(checkboxFor(collidingLabel).classList.contains('bg-blue-500')).toBe(false)

    mockPost.mockClear()
    await wrapper.find('[data-testid="save-selections"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))

    const saveCall = mockPost.mock.calls.find((c) => c[0] === '/user/selections')
    expect(saveCall).toBeDefined()
    const services = (saveCall![1] as { services: { category: string; service: string; checked: boolean }[] }).services
    const sent = (category: string, service: string) =>
      services.find((s) => s.category === category && s.service === service)?.checked

    expect(sent('a', 'b::c')).toBe(true)
    expect(sent('a::b', 'c')).toBe(false)
  })

  it('shows the right community badge for each of two category/service pairs that collide under a joined-string key', async () => {
    // (category "a", service "b|c") and (category "a|b", service "c")
    // would join to the same "a|b|c" under the old "category|service" key
    // scheme GetCommunities used — this is the same collision class as the
    // "::"-joined test above, but exercising the badges this field exists
    // to drive rather than selection tracking.
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
      catalog: { a: ['b|c'], 'a|b': ['c'] },
      selections: { categories: [], services: [] },
      communities: [
        { category: 'a', service: 'b|c', community: 10001 },
        { category: 'a|b', service: 'c', community: 20002 },
        { category: 'a|b', service: '', community: 20000 },
      ],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }
    mockUserDataGet(userData)
    mockPost.mockResolvedValue({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })

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

    const badgeTexts = wrapper.findAll('span[title]').map((s) => s.text())
    // Service "b|c" under category "a" must show its own community (10001),
    // not the one belonging to the colliding group "a|b" (20000).
    expect(badgeTexts).toContain('10001')
    // Group "a|b" must show its own group-level community (20000), not the
    // one belonging to the colliding service pair (a, b|c) -> 10001.
    expect(badgeTexts).toContain('20000')
    // Service "c" under category "a|b" must show its own community (20002).
    expect(badgeTexts).toContain('20002')
  })

  describe('address lookup', () => {
    const baseUserData = {
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
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }

    async function mountWithLookup(lookupHandler: (url: string) => Promise<{ data: unknown }>) {
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: baseUserData })
        if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
        if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
        if (url === '/user/debug') return lookupHandler(url)
        return Promise.reject(new Error(`unexpected GET ${url}`))
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
      return wrapper
    }

    it('runs an address lookup via the shared apiClient and renders matches', async () => {
      const result = {
        query: '8.8.8.0/24',
        matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
        before_percentage: 100,
        after_percentage: 100,
        in_tunnel: true,
      }
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: result }))

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      const debugCall = mockGet.mock.calls.find((c) => c[0] === '/user/debug')
      expect(debugCall).toBeDefined()
      expect(debugCall![1]).toEqual({ params: { cidr: '8.8.8.0/24' } })

      const resultEl = wrapper.find('[data-testid="lookup-result"]')
      expect(resultEl.exists()).toBe(true)
      expect(wrapper.findAll('[data-testid="lookup-match"]')).toHaveLength(1)
    })

    it('shows an error message when the lookup request fails', async () => {
      const wrapper = await mountWithLookup(() =>
        Promise.reject({ response: { status: 400, data: { error: 'invalid CIDR or IP address' } } }),
      )

      await wrapper.find('[data-testid="lookup-input"]').setValue('not-an-ip')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      expect(wrapper.find('[data-testid="lookup-error"]').text()).toBe('invalid CIDR or IP address')
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
    })

    it('treats a 401 from the lookup the same as any other session expiry', async () => {
      const wrapper = await mountWithLookup(() =>
        Promise.reject({ isAxiosError: true, response: { status: 401 } }),
      )

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // handleAuthError recognized it and flipped back to the login view —
      // not a local lookup error.
      expect(wrapper.find('[data-testid="lookup-error"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="lookup-input"]').exists()).toBe(false)
    })

    it('disables the check button until a query is entered', async () => {
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: { query: '', matches: [], before_percentage: 0, after_percentage: 0, in_tunnel: false } }))

      const button = wrapper.find('[data-testid="lookup-button"]')
      expect((button.element as HTMLButtonElement).disabled).toBe(true)

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      expect((button.element as HTMLButtonElement).disabled).toBe(false)

      await wrapper.find('[data-testid="lookup-input"]').setValue('   ')
      expect((button.element as HTMLButtonElement).disabled).toBe(true)
    })

    it('shows no-match message when matches is empty', async () => {
      const result = { query: '8.8.9.0/24', matches: [], before_percentage: 0, after_percentage: 0, in_tunnel: false }
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: result }))

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.9.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      expect(wrapper.findAll('[data-testid="lookup-match"]')).toHaveLength(0)
      // i18n messages are empty in this test setup, so the component
      // renders the raw key as fallback text — asserting on that confirms
      // the no-match branch rendered instead of the match list.
      expect(wrapper.find('[data-testid="lookup-result"]').text()).toContain('user.lookup_no_match')
    })

    it('renders two matches independently when category/service strings collide under a joined-string key', async () => {
      // (category "a", service "b::c") and (category "a::b", service "c")
      // join to the same string under a naive "category::service" key — the
      // component must iterate the matches array directly rather than
      // building any joined-string map, so both render independently.
      const result = {
        query: '8.8.8.0/24',
        matches: [
          { category: 'a', service: 'b::c', percentage: 100, selected: true },
          { category: 'a::b', service: 'c', percentage: 50, selected: false },
        ],
        before_percentage: 100,
        after_percentage: 100,
        in_tunnel: true,
      }
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: result }))

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      const rows = wrapper.findAll('[data-testid="lookup-match"]')
      expect(rows).toHaveLength(2)
      expect(rows[0].text()).toContain('a / b::c')
      expect(rows[1].text()).toContain('a::b / c')
    })

    it('resets lookup state on logout', async () => {
      const result = {
        query: '8.8.8.0/24',
        matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
        before_percentage: 100,
        after_percentage: 100,
        in_tunnel: true,
      }
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: result }))

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(true)

      mockPost.mockResolvedValue({ data: { ok: true } })
      await wrapper.find('[data-testid="logout-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // Logged out: the whole authenticated view (including the lookup
      // section) is gone, replaced by the login form.
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="lookup-input"]').exists()).toBe(false)
    })

    it('shows a distinct partial verdict instead of a binary "in tunnel" when only part of the queried range is delivered', async () => {
      // A /24 with only one selected /25 inside it: after_percentage lands
      // strictly between 0 and 100. Collapsing that to the same green
      // "in your tunnel" verdict as a fully-delivered query would imply the
      // whole queried range is routed when only half of it is.
      const result = {
        query: '8.8.8.0/24',
        matches: [{ category: 'AI', service: 'ChatGPT', percentage: 50, selected: true }],
        before_percentage: 50,
        after_percentage: 50,
        in_tunnel: true,
      }
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: result }))

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      const verdict = wrapper.find('[data-testid="lookup-in-tunnel"]')
      // i18n messages are empty in this test setup, so the component falls
      // back to the raw key — asserting on that confirms the partial branch
      // rendered, not the plain "full" in-tunnel one.
      expect(verdict.text()).toContain('user.lookup_partially_in_tunnel')
      expect(verdict.text()).not.toBe('user.lookup_in_tunnel')
    })

    it('ignores a stale lookup response that arrives after a newer one, instead of clobbering it', async () => {
      let resolveFirst: (v: { data: unknown }) => void = () => {}
      let resolveSecond: (v: { data: unknown }) => void = () => {}
      let calls = 0
      const wrapper = await mountWithLookup(() => {
        calls++
        if (calls === 1) return new Promise((resolve) => { resolveFirst = resolve })
        return new Promise((resolve) => { resolveSecond = resolve })
      })

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click') // call 1 (slow)

      // Edit the query and re-trigger via Enter while call 1 is still
      // pending — the button is disabled by then, but the input's Enter
      // handler is not gated on lookupLoading, which is exactly the gap
      // this test covers.
      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.9.0/24')
      await wrapper.find('[data-testid="lookup-input"]').trigger('keyup.enter') // call 2 (fresh)

      // Resolve the NEWER request first, then the stale one arrives late.
      resolveSecond({
        data: { query: '8.8.9.0/24', matches: [], before_percentage: 0, after_percentage: 0, in_tunnel: false },
      })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      resolveFirst({
        data: {
          query: '8.8.8.0/24',
          matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
          before_percentage: 100,
          after_percentage: 100,
          in_tunnel: true,
        },
      })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // Must still show the second (newer, empty-match) query's result, not
      // overwritten by the first request's late-arriving response.
      expect(wrapper.findAll('[data-testid="lookup-match"]')).toHaveLength(0)
      expect(wrapper.find('[data-testid="lookup-result"]').text()).toContain('user.lookup_no_match')
    })

    it('clears a stale lookup result after selections are saved', async () => {
      const lookupResultData = {
        query: '8.8.8.0/24',
        matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
        before_percentage: 100,
        after_percentage: 100,
        in_tunnel: true,
      }
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: { ...baseUserData, catalog: { AI: ['ChatGPT'] } } })
        if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
        if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
        if (url === '/user/debug') return Promise.resolve({ data: lookupResultData })
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPost.mockResolvedValue({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })

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

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(true)

      await wrapper.find('[data-testid="save-selections"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // The selection save may have changed what this address resolves to
      // — the previous answer is no longer trustworthy and must be gone.
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
    })

    it('clears a stale lookup result after a successful catalog mode switch', async () => {
      const lookupResultData = {
        query: '8.8.8.0/24',
        matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
        before_percentage: 100,
        after_percentage: 100,
        in_tunnel: true,
      }
      const modeAData = {
        ...baseUserData,
        user: { ...baseUserData.user, catalog_mode_id: 1, catalog_editable: true },
        modes: [
          { id: 1, name: 'Mode A', enabled: true, feed_count: 0 },
          { id: 2, name: 'Mode B', enabled: true, feed_count: 0 },
        ],
      }
      let meCalls = 0
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') {
          meCalls++
          return Promise.resolve({ data: modeAData })
        }
        if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
        if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
        if (url === '/user/debug') return Promise.resolve({ data: lookupResultData })
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPut.mockResolvedValue({ data: { ok: true } })
      mockPost.mockResolvedValue({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })

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

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(true)
      expect(meCalls).toBe(1)

      await wrapper.find('select').setValue('2')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      expect(meCalls).toBeGreaterThan(1) // reloadUserData refetched /user/me
      // The switch succeeded and reloaded data for a different mode — the
      // previous mode's lookup answer no longer applies.
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
    })

    it('formats a boundary-adjacent partial percentage without rounding to 0% or 100%, which would contradict the partial verdict', async () => {
      // A /24 minus one denied /32 is 99.609375% delivered; a single
      // covered /32 out of a /24 is 0.390625%. Plain Math.round would
      // render these as "100%" and "0%" respectively — each flatly
      // contradicting a "partial" label sitting right next to it.
      const result = {
        query: '8.8.8.0/24',
        matches: [
          { category: 'AI', service: 'ChatGPT', percentage: 99.609375, selected: true },
          { category: 'Other', service: 'Thing', percentage: 0.390625, selected: true },
        ],
        before_percentage: 100,
        after_percentage: 99.609375,
        in_tunnel: true,
      }
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: result }))

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      const rows = wrapper.findAll('[data-testid="lookup-match"]')
      expect(rows[0].text()).toContain('>99%')
      expect(rows[0].text()).not.toContain('100%')
      expect(rows[1].text()).toContain('<1%')
      expect(rows[1].text()).not.toContain('0%')
    })

    it('invalidates an in-flight lookup when a selection save succeeds while it is still pending', async () => {
      let resolveLookup: (v: { data: unknown }) => void = () => {}
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: { ...baseUserData, catalog: { AI: ['ChatGPT'] } } })
        if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
        if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
        if (url === '/user/debug') return new Promise((resolve) => { resolveLookup = resolve })
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPost.mockResolvedValue({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })

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

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click') // left pending, unresolved

      await wrapper.find('[data-testid="save-selections"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)

      // The stale in-flight lookup finally resolves AFTER the save already
      // cleared the state — without bumping the sequence, this would
      // silently repopulate it with the pre-save answer.
      resolveLookup({
        data: {
          query: '8.8.8.0/24',
          matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
          before_percentage: 100,
          after_percentage: 100,
          in_tunnel: true,
        },
      })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
    })

    it('clears the displayed result as soon as the query is edited, before resubmitting', async () => {
      const result = {
        query: '8.8.8.0/24',
        matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
        before_percentage: 100,
        after_percentage: 100,
        in_tunnel: true,
      }
      const wrapper = await mountWithLookup(() => Promise.resolve({ data: result }))

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(true)

      // Edit without resubmitting: the old answer no longer describes
      // what's in the box and must not linger underneath it.
      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.9.0/24')
      await wrapper.vm.$nextTick()

      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
    })

    it('invalidates a lookup result when the mode switch commits but the subsequent reload fails', async () => {
      // reloadUserData catches its own /user/me failure internally and
      // never reaches loadUserData on that path — so loadUserData's own
      // invalidation can't be relied on here. The /user/mode PUT above it
      // may have already committed the switch server-side regardless.
      const lookupResultData = {
        query: '8.8.8.0/24',
        matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
        before_percentage: 100,
        after_percentage: 100,
        in_tunnel: true,
      }
      const modeAData = {
        ...baseUserData,
        user: { ...baseUserData.user, catalog_mode_id: 1, catalog_editable: true },
        modes: [
          { id: 1, name: 'Mode A', enabled: true, feed_count: 0 },
          { id: 2, name: 'Mode B', enabled: true, feed_count: 0 },
        ],
      }
      let meCalls = 0
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') {
          meCalls++
          // First call (initial mount) succeeds; the reload triggered by
          // the mode switch fails.
          if (meCalls === 1) return Promise.resolve({ data: modeAData })
          return Promise.reject({ isAxiosError: true, response: { status: 500 } })
        }
        if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
        if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
        if (url === '/user/debug') return Promise.resolve({ data: lookupResultData })
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPut.mockResolvedValue({ data: { ok: true } }) // the mode PUT itself succeeds

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

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(true)

      await wrapper.find('select').setValue('2')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // The mode PUT succeeded even though the reload that followed it
      // failed — the stale, pre-switch lookup answer must still be gone.
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
    })

    it('invalidates a lookup result as soon as the selections save commits, without waiting for the count refresh that follows it', async () => {
      // fetchCounts is a second, independent network request issued after
      // the save already committed — if invalidation waited for it, a slow
      // or never-settling count refresh would leave the stale result
      // visible (and an in-flight lookup able to repopulate it) for as
      // long as that second request takes.
      let resolveCounts: (v: { data: unknown }) => void = () => {}
      let countCalls = 0
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: { ...baseUserData, catalog: { AI: ['ChatGPT'] } } })
        if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
        if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
        if (url === '/user/debug') return Promise.resolve({
          data: {
            query: '8.8.8.0/24',
            matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
            before_percentage: 100,
            after_percentage: 100,
            in_tunnel: true,
          },
        })
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPost.mockImplementation((url: string) => {
        if (url === '/user/selections') return Promise.resolve({ data: { ok: true } })
        if (url === '/user/count-prefixes') {
          countCalls++
          // The initial load's own count fetch must resolve normally —
          // only the one triggered by the save below is left pending.
          if (countCalls === 1) return Promise.resolve({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })
          return new Promise((resolve) => { resolveCounts = resolve })
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

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(true)

      await wrapper.find('[data-testid="save-selections"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // The count refresh triggered by the save is still pending — but the
      // selections POST has already resolved, which is all invalidation
      // should need.
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)

      resolveCounts({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
    })

    it('invalidates a lookup result even when a save reports failure, since the backend may have committed before failing on reconciliation', async () => {
      // apiUserSaveSelections/apiUserSaveFilters persist the submitted
      // configuration before BGP reconciliation runs, so a 500 here (like
      // switchMode's own documented "saved but reconciliation failed" case)
      // doesn't mean the selection didn't change.
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: { ...baseUserData, catalog: { AI: ['ChatGPT'] } } })
        if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
        if (url === '/user/feed-changes') return Promise.resolve({ data: { changes: [] } })
        if (url === '/user/debug') return Promise.resolve({
          data: {
            query: '8.8.8.0/24',
            matches: [{ category: 'AI', service: 'ChatGPT', percentage: 100, selected: true }],
            before_percentage: 100,
            after_percentage: 100,
            in_tunnel: true,
          },
        })
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPost.mockImplementation((url: string) => {
        if (url === '/user/selections') {
          return Promise.reject({
            isAxiosError: true,
            response: { status: 500, data: { error: 'Selection saved but BGP reconciliation failed: ...' } },
          })
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

      await wrapper.find('[data-testid="lookup-input"]').setValue('8.8.8.0/24')
      await wrapper.find('[data-testid="lookup-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(true)

      await wrapper.find('[data-testid="save-selections"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // The save itself rejected, but the previous lookup answer may now
      // describe a configuration that no longer applies.
      expect(wrapper.find('[data-testid="lookup-result"]').exists()).toBe(false)
    })
  })

  describe('filters in effect', () => {
    const baseUserData = {
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
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [],
    }

    async function mountWithRouteFilters(routeFiltersInfo: unknown) {
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: baseUserData })
        if (url === '/user/route-filters') return Promise.resolve({ data: routeFiltersInfo })
        return Promise.reject(new Error(`unexpected GET ${url}`))
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
      return wrapper
    }

    it('fetches and renders the filters in effect on load', async () => {
      const wrapper = await mountWithRouteFilters({
        mode: 'global',
        global: { allow: ['10.0.0.0/8'], deny: [] },
        own: { allow: [], deny: [] },
        effective: { allow: ['10.0.0.0/8'], deny: [] },
      })

      expect(mockGet).toHaveBeenCalledWith('/user/route-filters')
      const section = wrapper.find('[data-testid="route-filters-section"]')
      expect(section.exists()).toBe(true)
      expect(section.text()).toContain('user.route_filters_mode_global')
      expect(wrapper.find('[data-testid="route-filters-effective"]').text()).toContain('10.0.0.0/8')
    })

    it('shows the global/own breakdown only in extend mode', async () => {
      const wrapper = await mountWithRouteFilters({
        mode: 'extend',
        global: { allow: ['10.0.0.0/8'], deny: [] },
        own: { allow: ['192.168.0.0/16'], deny: [] },
        effective: { allow: ['10.0.0.0/8', '192.168.0.0/16'], deny: [] },
      })

      const globalBlock = wrapper.find('[data-testid="route-filters-global"]')
      const ownBlock = wrapper.find('[data-testid="route-filters-own"]')
      expect(globalBlock.exists()).toBe(true)
      expect(ownBlock.exists()).toBe(true)
      expect(globalBlock.text()).toContain('10.0.0.0/8')
      expect(ownBlock.text()).toContain('192.168.0.0/16')
    })

    it('shows no breakdown in global or override mode', async () => {
      const wrapper = await mountWithRouteFilters({
        mode: 'override',
        global: { allow: ['10.0.0.0/8'], deny: [] },
        own: { allow: ['192.168.0.0/16'], deny: [] },
        effective: { allow: ['192.168.0.0/16'], deny: [] },
      })

      expect(wrapper.find('[data-testid="route-filters-global"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="route-filters-own"]').exists()).toBe(false)
      expect(wrapper.find('[data-testid="route-filters-section"]').text()).toContain('user.route_filters_mode_override')
    })

    it('shows the empty-configuration hint when a list has no entries', async () => {
      const wrapper = await mountWithRouteFilters({
        mode: 'global',
        global: { allow: [], deny: [] },
        own: { allow: [], deny: [] },
        effective: { allow: [], deny: [] },
      })

      expect(wrapper.find('[data-testid="route-filters-effective"]').text()).toContain('user.route_filters_empty')
    })

    it('resets filters-in-effect state on logout', async () => {
      const wrapper = await mountWithRouteFilters({
        mode: 'global',
        global: { allow: ['10.0.0.0/8'], deny: [] },
        own: { allow: [], deny: [] },
        effective: { allow: ['10.0.0.0/8'], deny: [] },
      })
      expect(wrapper.find('[data-testid="route-filters-section"]').exists()).toBe(true)

      mockPost.mockResolvedValue({ data: { ok: true } })
      await wrapper.find('[data-testid="logout-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      expect(wrapper.find('[data-testid="route-filters-section"]').exists()).toBe(false)
    })

    it('renders null allow/deny lists (as Go serializes an empty slice) without crashing', async () => {
      // RouteFilters.Allow/Deny are nil slices when empty; encoding/json
      // marshals a nil slice as null, not [] — a real API response, not
      // just a sloppy test fixture.
      const wrapper = await mountWithRouteFilters({
        mode: 'global',
        global: { allow: null, deny: null },
        own: { allow: null, deny: null },
        effective: { allow: null, deny: null },
      })

      expect(wrapper.find('[data-testid="route-filters-section"]').exists()).toBe(true)
      expect(wrapper.find('[data-testid="route-filters-effective"]').text()).toContain('user.route_filters_empty')
    })

    it('drops a stale route-filters response from a save that resolves after a later mode switch already fetched a newer one', async () => {
      // A real, user-reachable overlap: saveFilters's own post-save refetch
      // is slow, and the mode switcher isn't disabled while it's in
      // flight (only the save button is), so the user can switch modes
      // before it lands — that resync's refetch must win.
      const userData = {
        ...baseUserData,
        user: { ...baseUserData.user, catalog_mode_id: 1, catalog_editable: true, filter_editable: true },
        modes: [
          { id: 1, name: 'Mode A', enabled: true, feed_count: 0 },
          { id: 2, name: 'Mode B', enabled: true, feed_count: 0 },
        ],
      }
      let resolveStale: (v: { data: unknown }) => void = () => {}
      let routeFiltersCalls = 0
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: userData })
        if (url === '/user/route-filters') {
          routeFiltersCalls++
          if (routeFiltersCalls === 1) {
            return Promise.resolve({
              data: { mode: 'global', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
            })
          }
          if (routeFiltersCalls === 2) return new Promise((resolve) => { resolveStale = resolve }) // triggered by saveFilters below
          return Promise.resolve({
            data: { mode: 'extend', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
          })
        }
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPost.mockImplementation((url: string) => {
        if (url === '/user/filters') return Promise.resolve({ data: { ok: true } })
        return Promise.resolve({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })
      })
      mockPut.mockResolvedValue({ data: { ok: true } })

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
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).toContain('user.route_filters_mode_global')

      // Save filters — its post-save refetch (call #2) is left pending.
      const saveButton = wrapper.findAll('button').find((b) => b.text().includes('user.save_filters'))
      await saveButton?.trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // Switch mode before the save's own refetch lands — its resync
      // (call #3) resolves immediately.
      await wrapper.find('select').setValue('2')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).toContain('user.route_filters_mode_extend')

      // The stale save-triggered refetch finally resolves — without the
      // sequencing guard this would silently clobber the mode switch's
      // newer answer.
      resolveStale({
        data: { mode: 'override', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
      })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).toContain('user.route_filters_mode_extend')
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).not.toContain('user.route_filters_mode_override')
    })

    it('refreshes the filters in effect after a self-service filter save', async () => {
      let routeFiltersCalls = 0
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') {
          return Promise.resolve({
            data: { ...baseUserData, user: { ...baseUserData.user, filter_editable: true } },
          })
        }
        if (url === '/user/route-filters') {
          routeFiltersCalls++
          return Promise.resolve({
            data: {
              mode: 'override',
              global: { allow: [], deny: [] },
              own: { allow: [], deny: [] },
              effective: { allow: routeFiltersCalls === 1 ? [] : ['192.168.0.0/16'], deny: [] },
            },
          })
        }
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      mockPost.mockImplementation((url: string) => {
        if (url === '/user/filters') return Promise.resolve({ data: { ok: true } })
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
      expect(wrapper.find('[data-testid="route-filters-effective"]').text()).toContain('user.route_filters_empty')

      await wrapper.find('#ufallow').setValue('192.168.0.0/16')
      const saveButton = wrapper.findAll('button').find((b) => b.text().includes('user.save_filters'))
      await saveButton?.trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      expect(routeFiltersCalls).toBe(2)
      expect(wrapper.find('[data-testid="route-filters-effective"]').text()).toContain('192.168.0.0/16')
    })

    it('never shows a previous user\'s stale filters to the next user who logs in on the same page', async () => {
      // User A has a route-filters request in flight (triggered by a
      // filter save) when they log out; user B logs in while B's own
      // count-prefixes fetch is still pending — loadUserData awaits that
      // before starting B's own route-filters fetch. A's stale response
      // must never become visible in B's session, however it resolves.
      const aliceData = { ...baseUserData, user: { ...baseUserData.user, filter_editable: true } }
      const bobData = {
        ...baseUserData,
        user: { ...baseUserData.user, id: 2, name: 'Bob' },
      }
      let resolveAliceStale: (v: { data: unknown }) => void = () => {}
      let resolveBobCounts: (v: { data: unknown }) => void = () => {}
      let routeFiltersCalls = 0
      let meCalls = 0
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') {
          meCalls++
          return Promise.resolve({ data: aliceData }) // only the initial mount uses /user/me here
        }
        if (url === '/user/route-filters') {
          routeFiltersCalls++
          if (routeFiltersCalls === 1) {
            return Promise.resolve({
              data: { mode: 'global', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
            })
          }
          if (routeFiltersCalls === 2) return new Promise((resolve) => { resolveAliceStale = resolve }) // Alice's save-triggered refetch
          return Promise.resolve({
            data: { mode: 'override', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
          }) // Bob's own refetch
        }
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      let countCalls = 0
      mockPost.mockImplementation((url: string) => {
        if (url === '/user/filters') return Promise.resolve({ data: { ok: true } })
        if (url === '/user/logout') return Promise.resolve({ data: { ok: true } })
        if (url === '/user/login') return Promise.resolve({ data: bobData })
        if (url === '/user/count-prefixes') {
          countCalls++
          if (countCalls === 1) return Promise.resolve({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } }) // Alice's initial load
          return new Promise((resolve) => { resolveBobCounts = resolve }) // Bob's own, left pending
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
      expect(meCalls).toBe(1)
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).toContain('user.route_filters_mode_global')

      // Alice saves her filters — the post-save refetch (call #2) is left pending.
      const saveButton = wrapper.findAll('button').find((b) => b.text().includes('user.save_filters'))
      await saveButton?.trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // Alice logs out while that refetch is still in flight.
      await wrapper.find('[data-testid="logout-button"]').trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-section"]').exists()).toBe(false)

      // Bob logs in; his own count-prefixes fetch is left pending, so his
      // route-filters fetch (call #3) hasn't started yet.
      await wrapper.find('input[type="text"]').setValue('bob')
      await wrapper.find('input[type="password"]').setValue('secret')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // Alice's stale refetch resolves now — it must not reappear in Bob's
      // session even though Bob's own fetch hasn't fired yet.
      resolveAliceStale({
        data: { mode: 'extend', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
      })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-section"]').exists()).toBe(false)

      // Bob's own count fetch resolves, unblocking his own route-filters
      // fetch (call #3) — his real data must display correctly.
      resolveBobCounts({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).toContain('user.route_filters_mode_override')
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).not.toContain('user.route_filters_mode_extend')
    })

    it('never shows a previous user\'s stale filters after a session expiry (401), not just an explicit logout', async () => {
      // Same leak as above, but the session ends via a 401 picked up by
      // handleAuthError (e.g. the mode-switch PUT below) rather than the
      // user clicking "logout" — handleAuthError must invalidate the same
      // state handleLogout does, since any authenticated action can hit a
      // 401 once a session expires.
      const aliceData = {
        ...baseUserData,
        user: { ...baseUserData.user, catalog_mode_id: 1, catalog_editable: true, filter_editable: true },
        modes: [
          { id: 1, name: 'Mode A', enabled: true, feed_count: 0 },
          { id: 2, name: 'Mode B', enabled: true, feed_count: 0 },
        ],
      }
      const bobData = { ...baseUserData, user: { ...baseUserData.user, id: 2, name: 'Bob' } }
      let resolveAliceStale: (v: { data: unknown }) => void = () => {}
      let resolveBobCounts: (v: { data: unknown }) => void = () => {}
      let routeFiltersCalls = 0
      mockGet.mockImplementation((url: string) => {
        if (url === '/user/me') return Promise.resolve({ data: aliceData }) // only the initial mount uses /user/me here
        if (url === '/user/route-filters') {
          routeFiltersCalls++
          if (routeFiltersCalls === 1) {
            return Promise.resolve({
              data: { mode: 'global', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
            })
          }
          if (routeFiltersCalls === 2) return new Promise((resolve) => { resolveAliceStale = resolve }) // Alice's save-triggered refetch
          return Promise.resolve({
            data: { mode: 'override', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
          }) // Bob's own refetch
        }
        return Promise.reject(new Error(`unexpected GET ${url}`))
      })
      let countCalls = 0
      mockPost.mockImplementation((url: string) => {
        if (url === '/user/filters') return Promise.resolve({ data: { ok: true } })
        if (url === '/user/login') return Promise.resolve({ data: bobData })
        if (url === '/user/count-prefixes') {
          countCalls++
          if (countCalls === 1) return Promise.resolve({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } }) // Alice's initial load
          return new Promise((resolve) => { resolveBobCounts = resolve }) // Bob's own, left pending
        }
        return Promise.resolve({ data: {} })
      })
      mockPut.mockRejectedValue({ isAxiosError: true, response: { status: 401 } })

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
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).toContain('user.route_filters_mode_global')

      // Alice saves her filters — the post-save refetch (call #2) is left pending.
      const saveButton = wrapper.findAll('button').find((b) => b.text().includes('user.save_filters'))
      await saveButton?.trigger('click')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // Alice's session expires via a 401 on an unrelated action (a mode
      // switch) while that refetch is still in flight — not an explicit
      // logout.
      await wrapper.find('select').setValue('2')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-section"]').exists()).toBe(false)

      // Bob logs in; his own count-prefixes fetch is left pending, so his
      // route-filters fetch (call #3) hasn't started yet.
      await wrapper.find('input[type="text"]').setValue('bob')
      await wrapper.find('input[type="password"]').setValue('secret')
      await wrapper.find('form').trigger('submit')
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()

      // Alice's stale refetch resolves now — it must not reappear in Bob's
      // session even though Bob's own fetch hasn't fired yet.
      resolveAliceStale({
        data: { mode: 'extend', global: { allow: [], deny: [] }, own: { allow: [], deny: [] }, effective: { allow: [], deny: [] } },
      })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-section"]').exists()).toBe(false)

      // Bob's own count fetch resolves, unblocking his own route-filters
      // fetch (call #3) — his real data must display correctly.
      resolveBobCounts({ data: { v4: 0, v6: 0, delta_v4: 0, delta_v6: 0 } })
      await new Promise((r) => setTimeout(r, 0))
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).toContain('user.route_filters_mode_override')
      expect(wrapper.find('[data-testid="route-filters-mode"]').text()).not.toContain('user.route_filters_mode_extend')
    })
  })

  it('shows feed-driven growth and acknowledges up to the newest sync on dismiss', async () => {
    const userData = {
      user: { id: 1, name: 'Alice', catalog_mode_id: 1, catalog_mode_name: 'Mode A', selection_locked: false,
        filter_editable: false, filter_override: false, filter_mode: 'allow', catalog_editable: true, networks: [] },
      catalog: {}, selections: { categories: ['ai'], services: [] }, communities: [],
      prefix_counts: { v4: {}, v6: {} }, filters: { allow: [], deny: [] },
      modes: [{ id: 1, name: 'Mode A', enabled: true, feed_count: 1 }],
    }
    const changes = [
      { change_id: 101, mode_id: 1, feed_name: 'opencck-main', synced_at: 1700000100, categories: [{ category: 'ai', added_services: 2 }] },
      { change_id: 102, mode_id: 1, feed_name: 'opencck-main', synced_at: 1700000100, categories: [{ category: 'ai', added_services: 1 }] },
    ]
    mockGet.mockImplementation((url: string) => {
      if (url === '/user/feed-changes') return Promise.resolve({ data: { changes } })
      if (url === '/user/route-filters') return Promise.resolve({ data: defaultRouteFiltersInfo })
      return Promise.resolve({ data: userData })
    })
    mockPost.mockResolvedValue({ data: { ok: true } })

    const UserPage = (await import('../UserPage.vue')).default
    const wrapper = mount(UserPage, {
      global: {
        plugins: [i18n, PrimeVue],
        stubs: {
          LanguageSwitcher: { template: '<div />' },
          Toast: { template: '<div />' },
        },
      },
    })
    await new Promise((r) => setTimeout(r, 0))
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="feed-changes-section"]').exists()).toBe(true)
    await wrapper.find('[data-testid="feed-changes-dismiss"]').trigger('click')
    await new Promise((r) => setTimeout(r, 0))

    expect(mockPost).toHaveBeenCalledWith('/user/feed-changes/ack', { mode_id: 1, through: 102 })
    expect(wrapper.find('[data-testid="feed-changes-section"]').exists()).toBe(false)
  })
})
