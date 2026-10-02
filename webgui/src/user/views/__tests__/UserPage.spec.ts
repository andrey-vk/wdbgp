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
      communities: [],
      prefix_counts: { v4: {}, v6: {} },
      filters: { allow: [], deny: [] },
      modes: [
        { id: 1, name: 'Mode A', enabled: true, feed_count: 0 },
        { id: 2, name: 'Mode B', enabled: true, feed_count: 0 },
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
      communities: [],
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
      communities: [],
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
      communities: [],
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
      communities: [],
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
        communities: [],
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
    mockGet.mockResolvedValue({ data: userData })
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
    mockGet.mockResolvedValue({ data: userData })
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
  })
})
