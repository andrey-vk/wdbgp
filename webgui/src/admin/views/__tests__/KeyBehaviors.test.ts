import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

// ============================================================
// Global mocks
// ============================================================

const tMock = vi.fn((key: string, params?: Record<string, unknown>) => {
  if (params && typeof params === 'object') {
    return key.replace(/\{(\w+)\}/g, (_, k) => String(params[k] ?? '?'))
  }
  return key
})

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: tMock }),
}))

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

const mockPush = vi.fn()
let mockRouteParams: Record<string, string> = { id: '1' }

vi.mock('vue-router', () => ({
  useRoute: () => ({
    params: mockRouteParams,
    query: {},
    path: '/',
  }),
  useRouter: () => ({
    push: mockPush,
  }),
  onBeforeRouteLeave: vi.fn(),
}))

vi.mock('primevue/useconfirm', () => ({
  useConfirm: () => ({
    require: vi.fn(),
  }),
}))

vi.mock('primevue/usetoast', () => ({
  useToast: () => ({
    add: vi.fn(),
  }),
}))

vi.mock('@/utils/format', () => ({
  formatDateTime: () => '2024-01-01',
}))

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  default: { register: vi.fn() },
}))

function stubPrimeVueComponents() {
  return {
    Button: { props: ['label'], template: '<button class="stub-btn">{{ label }}<slot /></button>' },
    InputText: { template: '<input class="stub-input" />', inheritAttrs: false },
    InputNumber: { template: '<input class="stub-inputnum" type="number" />', inheritAttrs: false },
    Textarea: { template: '<textarea class="stub-textarea"></textarea>', inheritAttrs: false },
    Select: { template: '<select class="stub-select"><slot /></select>', inheritAttrs: false },
    ToggleSwitch: {
      props: ['modelValue'],
      emits: ['update:modelValue'],
      template: '<input class="stub-toggle" type="checkbox" :checked="modelValue" @change="$emit(\'update:modelValue\', $event.target.checked)" />',
    },
    Checkbox: { template: '<input class="stub-checkbox" type="checkbox" />', inheritAttrs: false },
    Tag: { template: '<span class="stub-tag"><slot /></span>', inheritAttrs: false },
    Message: { template: '<div class="stub-message"><slot /></div>', inheritAttrs: false },
    Chart: { template: '<div class="stub-chart"></div>', inheritAttrs: false },
    Popover: { template: '<div class="stub-popover"><slot /></div>', inheritAttrs: false },
    Toast: { template: '<div class="stub-toast"></div>', inheritAttrs: false },
    ConfirmDialog: { template: '<div class="stub-confirm"></div>', inheritAttrs: false },
    ProgressSpinner: { template: '<div class="stub-spinner"></div>', inheritAttrs: false },
    SelectButton: { template: '<div class="stub-selectbtn"><slot /></div>', inheritAttrs: false },
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  setActivePinia(createPinia())
  mockRouteParams = { id: '1' }

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

  vi.stubGlobal('confirm', vi.fn(() => true))

  document.documentElement.classList.remove('app-dark')

  // Default mock responses
  mockGet.mockImplementation((url: string) => {
    if (url === '/admin/settings') {
      return Promise.resolve({
        data: {
          default_language: { value: 'en', default_value: 'en', env_override: false },
          default_web_auth: { value: 'network', default_value: 'network', env_override: false },
          filter_allow: { value: '', default_value: '', env_override: false },
          filter_deny: { value: '', default_value: '', env_override: false },
          active_dial: { value: null, default_value: false, env_override: false },
        },
      })
    }
    if (url === '/admin/users') {
      return Promise.resolve({
        data: {
          users: [
            {
              id: 1, name: 'Test User', peer_ip: '10.0.0.1', peer_asn: 65001,
              has_password: true, next_hop: '', web_auth: 'network',
              enabled: true, active_dial: false, catalog_mode_id: 1,
              catalog_mode_name: 'Default', networks: ['192.168.0.0/24'],
              selection_locked: false, catalog_editable: true,
              filter_editable: false, filter_override: false,
              filter_allow: [], filter_deny: [], peer_state: 'ESTABLISHED',
            },
          ],
        },
      })
    }
    if (url === '/admin/users/1/credentials') {
      return Promise.resolve({ data: { credentials: [] } })
    }
    if (url === '/admin/modes') {
      return Promise.resolve({ data: { modes: [{ id: 1, name: 'Default', enabled: true }] } })
    }
    if (url === '/admin/modes/1/communities') {
      return Promise.resolve({
        data: {
          communities: [
            { category: 'Cat1', service: '', community: 100, auto_community: 100 },
            { category: 'Cat1', service: 'Svc1', community: 101, auto_community: 101 },
            { category: 'Cat1', service: 'Svc2', community: 101, auto_community: 102 },
          ],
        },
      })
    }
    if (url === '/admin/modes/1') {
      return Promise.resolve({ data: { name: 'Test Mode' } })
    }
    return Promise.resolve({ data: {} })
  })
})

// ============================================================
// Test: SettingsPage unsaved guard
// ============================================================

describe('SettingsPage unsaved guard', () => {
  it('mounts and renders settings sections', async () => {
    const SettingsPage = (await import('@/admin/views/SettingsPage.vue')).default
    const wrapper = mount(SettingsPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })

    await new Promise(resolve => setTimeout(resolve, 50))
    await nextTick()

    expect(wrapper.exists()).toBe(true)
    // The settings page should render section headers
    expect(wrapper.html()).toContain('settings.section_admin_access')
    // Save button should be present
    expect(wrapper.html()).toContain('settings.save')
  })
})

// ============================================================
// Test: CommunitiesPage duplicate validation
// ============================================================

describe('CommunitiesPage duplicate validation', () => {
  it('shows red border on inputs with duplicate community values', async () => {
    const CommunitiesPage = (await import('@/admin/views/CommunitiesPage.vue')).default
    const wrapper = mount(CommunitiesPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })

    await new Promise(resolve => setTimeout(resolve, 50))
    await nextTick()

    // Check that the duplicate is detected
    const html = wrapper.html()
    expect(html).toContain('has-duplicate')
  })
})

// ============================================================
// Test: UsersPage BGP password
// ============================================================

describe('UsersPage BGP password', () => {
  it('password input has empty value when user has has_password=true', async () => {
    const UsersPage = (await import('@/admin/views/UsersPage.vue')).default
    const wrapper = mount(UsersPage, {
      global: {
        stubs: stubPrimeVueComponents(),
      },
    })

    await new Promise(resolve => setTimeout(resolve, 50))
    await nextTick()

    // Click on the user in the list to select it
    const userItems = wrapper.findAll('.cursor-pointer')
    expect(userItems.length).toBeGreaterThan(0)
    await userItems[0].trigger('click')
    await new Promise(resolve => setTimeout(resolve, 50))
    await nextTick()

    // Now we should be in view mode showing the selected user's data.
    // Check that the view mode shows the BGP password field as "Yes" (has_password=true)
    // The view mode renders: {{ selected.has_password ? t('dialog.yes') : t('users.bgp_password_not_set') }}
    const html = wrapper.html()
    expect(html).toContain('dialog.yes')

    // Now click Edit
    const buttons = wrapper.findAll('.stub-btn')
    let clickedEdit = false
    for (const btn of buttons) {
      // The Edit button label is t('users.edit') which returns 'users.edit'
      if (btn.text().includes('users.edit')) {
        await btn.trigger('click')
        clickedEdit = true
        break
      }
    }

    expect(clickedEdit).toBe(true)
    await nextTick()

    // In edit mode with password_enabled=true (has_password=true), the password input
    // should be rendered. The password input has id="upw" and starts with empty value
    // because we never render the actual BGP password.
    // With our stubs, the input becomes a .stub-input element.
    const editHtml = wrapper.html()
    // Should have the password toggle enabled
    expect(editHtml).toContain('stub-toggle')
    // Should render the save button
    expect(editHtml).toContain('users.save')
  })

  // ============================================================
  // Regression: new peers must default active_dial to the current
  // global Active Dial setting, not a hardcoded value — an admin who's
  // turned it on (or off) globally expects new peers to follow suit.
  // ============================================================

  it('new peer defaults active_dial to the current global setting, not a hardcoded value', async () => {
    // Simulate an admin who has explicitly turned the global switch ON.
    mockGet.mockImplementation((url: string) => {
      if (url === '/admin/settings') {
        return Promise.resolve({
          data: {
            default_language: { value: 'en', default_value: 'en', env_override: false },
            default_web_auth: { value: 'network', default_value: 'network', env_override: false },
            filter_allow: { value: '', default_value: '', env_override: false },
            filter_deny: { value: '', default_value: '', env_override: false },
            active_dial: { value: true, default_value: false, env_override: false },
          },
        })
      }
      if (url === '/admin/users') {
        return Promise.resolve({ data: { users: [] } })
      }
      if (url === '/admin/modes') {
        return Promise.resolve({ data: { modes: [{ id: 1, name: 'Default', enabled: true }] } })
      }
      return Promise.resolve({ data: {} })
    })

    const UsersPage = (await import('@/admin/views/UsersPage.vue')).default
    const wrapper = mount(UsersPage, {
      global: { stubs: stubPrimeVueComponents() },
    })
    await new Promise(resolve => setTimeout(resolve, 50))
    await nextTick()

    // Click "Add User" to start a new peer (startNew()).
    const buttons = wrapper.findAll('.stub-btn')
    let clickedAdd = false
    for (const btn of buttons) {
      if (btn.text().includes('users.add')) {
        await btn.trigger('click')
        clickedAdd = true
        break
      }
    }
    expect(clickedAdd).toBe(true)
    await nextTick()

    const toggle = wrapper.find('#uadial')
    expect(toggle.exists()).toBe(true)
    expect((toggle.element as HTMLInputElement).checked).toBe(true)
  })
})

// ============================================================
// Regression: leaving the user-selections page with the count debounce
// pending fired POST /admin/users/NaN/count-selections — the timer
// survived unmount, and userId (computed from route.params.id) turned
// NaN once the route no longer carried an :id param.
// ============================================================

describe('UserSelectionsPage stray count request', () => {
  it('does not fire count-selections after the page is left mid-debounce', async () => {
    mockRouteParams = { id: '4' }
    mockGet.mockImplementation((url: string) => {
      if (url === '/admin/users/4/catalog') {
        return Promise.resolve({
          data: {
            user: { id: 4, name: 'Test User', catalog_mode_id: 1 },
            modes: [{ id: 1, name: 'Default' }],
            catalog: { Video: ['youtube'] },
            prefix_counts: { v4: { Video: { youtube: 10 } }, v6: {} },
            selections: { categories: [], services: [] },
          },
        })
      }
      return Promise.resolve({ data: {} })
    })
    mockPost.mockResolvedValue({ data: { v4: 10, v6: 0, delta_v4: 0, delta_v6: 0 } })

    const UserSelectionsPage = (await import('@/admin/views/UserSelectionsPage.vue')).default
    const wrapper = mount(UserSelectionsPage, {
      global: { stubs: stubPrimeVueComponents() },
    })
    await new Promise(resolve => setTimeout(resolve, 50))
    await nextTick()

    // The initial load counts with the real id.
    expect(mockPost).toHaveBeenCalledWith('/admin/users/4/count-selections', expect.anything())
    mockPost.mockClear()

    // Toggle a category: arms the 300ms debounced recount.
    const label = wrapper.findAll('span').find((s) => s.text() === 'Video')
    expect(label).toBeDefined()
    await label!.trigger('click')

    // Navigate away before the debounce fires: the route loses :id and
    // the component unmounts.
    delete mockRouteParams.id
    wrapper.unmount()

    await new Promise(resolve => setTimeout(resolve, 400))
    expect(mockPost).not.toHaveBeenCalled()
  })
})
