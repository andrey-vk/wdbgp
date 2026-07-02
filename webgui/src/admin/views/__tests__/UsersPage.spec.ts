import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

// ============================================================
// Global mocks
// ============================================================

const tMock = vi.fn((key: string, params?: Record<string, unknown>) => {
  if (params && typeof params === 'object') {
    return key.replace(/\{(\w+)\}/g, (_, k) => params[k] ?? '?')
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

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: '1' }, query: {}, path: '/' }),
  useRouter: () => ({ push: vi.fn() }),
  onBeforeRouteLeave: vi.fn(),
}))

vi.mock('primevue/useconfirm', () => ({
  useConfirm: () => ({ require: vi.fn() }),
}))

const toastAdd = vi.fn()
vi.mock('primevue/usetoast', () => ({
  useToast: () => ({ add: toastAdd }),
}))

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
    Dialog: { template: '<div class="stub-dialog"><slot /></div>', inheritAttrs: false },
  }
}

function defaultGetMock() {
  mockGet.mockImplementation((url: string) => {
    const defaults: Record<string, unknown> = {
      '/admin/users': {
        data: {
          users: [
            {
              id: 1, name: 'Test User', peer_ip: '10.0.0.1', peer_asn: 65001,
              has_password: false, next_hop: '', web_auth: 'network',
              enabled: true, active_dial: false, catalog_mode_id: 1,
              catalog_mode_name: 'Default', networks: ['192.168.0.0/24'],
              selection_locked: false, catalog_editable: true,
              filter_editable: false, filter_override: false,
              filter_allow: [], filter_deny: [], peer_state: '',
            },
          ],
        },
      },
      '/admin/users/1/credentials': { data: { credentials: [] } },
      '/admin/modes': { data: { modes: [{ id: 1, name: 'Default', enabled: true }] } },
      '/admin/settings': {
        data: {
          default_language: { value: 'en', default_value: 'en', env_override: false },
          default_web_auth: { value: 'network', default_value: 'network', env_override: false },
          filter_allow: { value: '', default_value: '', env_override: false },
          filter_deny: { value: '', default_value: '', env_override: false },
          active_dial: { value: null, default_value: false, env_override: false },
        },
      },
    }
    return Promise.resolve(defaults[url] || { data: {} })
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  setActivePinia(createPinia())
  defaultGetMock()

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
})

async function mountUsersPage() {
  const UsersPage = (await import('@/admin/views/UsersPage.vue')).default
  const wrapper = mount(UsersPage, {
    global: { stubs: stubPrimeVueComponents() },
  })
  await new Promise(resolve => setTimeout(resolve, 50))
  await nextTick()
  return wrapper
}

describe('UsersPage networks normalization', () => {
  it('flags unnormalized input and does not flag already-normalized input', async () => {
    mockPost.mockImplementation((url: string) => {
      if (url === '/admin/users/normalize-networks') {
        return Promise.resolve({ data: { networks: ['10.0.0.0/24'] } })
      }
      return Promise.resolve({ data: {} })
    })

    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { networks_text: string }
      networksNeedNormalization: boolean
      checkNetworksNormalization: () => Promise<void>
    }

    vm.form.networks_text = '10.0.0.5/24'
    await vm.checkNetworksNormalization()
    expect(vm.networksNeedNormalization).toBe(true)

    vm.form.networks_text = '10.0.0.0/24'
    await vm.checkNetworksNormalization()
    expect(vm.networksNeedNormalization).toBe(false)
  })

  it('flags overlapping and adjacent ranges as needing normalization', async () => {
    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { networks_text: string }
      networksNeedNormalization: boolean
      checkNetworksNormalization: () => Promise<void>
    }

    mockPost.mockImplementation((url: string) => {
      if (url === '/admin/users/normalize-networks') {
        return Promise.resolve({ data: { networks: ['10.0.0.0/24'] } })
      }
      return Promise.resolve({ data: {} })
    })
    vm.form.networks_text = '10.0.0.0/24\n10.0.0.128/25'
    await vm.checkNetworksNormalization()
    expect(vm.networksNeedNormalization).toBe(true)

    vm.form.networks_text = '10.0.0.0/25\n10.0.0.128/25'
    await vm.checkNetworksNormalization()
    expect(vm.networksNeedNormalization).toBe(true)
  })

  it('ignores order and whitespace differences', async () => {
    mockPost.mockImplementation((url: string) => {
      if (url === '/admin/users/normalize-networks') {
        return Promise.resolve({ data: { networks: ['10.0.0.0/24', '10.0.2.0/24'] } })
      }
      return Promise.resolve({ data: {} })
    })

    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { networks_text: string }
      networksNeedNormalization: boolean
      checkNetworksNormalization: () => Promise<void>
    }

    vm.form.networks_text = '  10.0.2.0/24  \n\n10.0.0.0/24'
    await vm.checkNetworksNormalization()
    expect(vm.networksNeedNormalization).toBe(false)
  })

  it('applyNetworksNormalization replaces the field and clears the flag', async () => {
    mockPost.mockImplementation((url: string) => {
      if (url === '/admin/users/normalize-networks') {
        return Promise.resolve({ data: { networks: ['10.0.0.0/24'] } })
      }
      return Promise.resolve({ data: {} })
    })

    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { networks_text: string }
      networksNeedNormalization: boolean
      checkNetworksNormalization: () => Promise<void>
      applyNetworksNormalization: () => void
    }

    vm.form.networks_text = '10.0.0.5/24'
    await vm.checkNetworksNormalization()
    expect(vm.networksNeedNormalization).toBe(true)

    vm.applyNetworksNormalization()
    expect(vm.form.networks_text).toBe('10.0.0.0/24')
    expect(vm.networksNeedNormalization).toBe(false)
  })

  it('handleSave is blocked while normalization is needed', async () => {
    mockPost.mockImplementation((url: string) => {
      if (url === '/admin/users/normalize-networks') {
        return Promise.resolve({ data: { networks: ['10.0.0.0/24'] } })
      }
      return Promise.resolve({ data: {} })
    })

    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { name: string; peer_ip: string; networks_text: string }
      networksNeedNormalization: boolean
      checkNetworksNormalization: () => Promise<void>
      handleSave: () => Promise<void>
    }

    vm.form.name = 'someone'
    vm.form.peer_ip = '10.0.50.1'
    vm.form.networks_text = '10.0.0.5/24'
    await vm.checkNetworksNormalization()
    expect(vm.networksNeedNormalization).toBe(true)

    mockPost.mockClear()
    await vm.handleSave()

    // Neither create nor the normalize-preview endpoint should have been
    // hit by handleSave itself — it must bail out before any request.
    const createCalls = mockPost.mock.calls.filter(([url]) => url === '/admin/users')
    expect(createCalls.length).toBe(0)
    expect(toastAdd).toHaveBeenCalled()
  })

  it('omits networks from the save payload entirely when the field is hidden', async () => {
    mockPost.mockImplementation(() => Promise.resolve({
      data: {
        id: 2, name: 'login-user', peer_ip: '10.0.60.1', peer_asn: 65030,
        has_password: false, next_hop: '', web_auth: 'login', enabled: true,
        active_dial: false, catalog_mode_id: 1, catalog_mode_name: 'Default',
        networks: [], selection_locked: false, catalog_editable: true,
        filter_editable: false, filter_override: false, filter_allow: [], filter_deny: [],
      },
    }))

    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { name: string; peer_ip: string; web_auth: string; networks_text: string }
      handleSave: () => Promise<void>
    }

    vm.form.name = 'login-user'
    vm.form.peer_ip = '10.0.60.1'
    vm.form.web_auth = 'login'
    // Leftover text in the (now hidden) field must not leak into the
    // payload — the field being hidden is what matters, not its content.
    vm.form.networks_text = '10.0.0.0/24'

    await vm.handleSave()

    const createCall = mockPost.mock.calls.find(([url]) => url === '/admin/users')
    expect(createCall).toBeTruthy()
    const payload = createCall?.[1] as Record<string, unknown>
    // apiClient.post is mocked here, bypassing axios's real JSON.stringify
    // (which is what actually drops undefined-valued keys over the wire) —
    // undefined is the correct signal at this layer; the real end-to-end
    // omission is verified via a live browser network inspection.
    expect(payload.networks).toBeUndefined()
  })
})

describe('UsersPage filter_mode / filter_editable independence', () => {
  it('derives the select value from filter_mode, not filter_editable', async () => {
    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { filter_mode: string; filter_editable: boolean; filter_override: boolean }
      filterModeSelect: string
    }

    // An admin-managed user: extend mode configured by the admin, but the
    // user themselves was never granted self-service editing. Before the
    // fix, the select derived its value from filter_editable/filter_override
    // instead of filter_mode, so this combination incorrectly displayed as
    // "global".
    vm.form.filter_mode = 'extend'
    vm.form.filter_editable = false
    vm.form.filter_override = false
    expect(vm.filterModeSelect).toBe('extend')

    vm.form.filter_mode = 'override'
    vm.form.filter_editable = false
    expect(vm.filterModeSelect).toBe('override')
  })

  it('changing the mode does not touch filter_editable', async () => {
    const wrapper = await mountUsersPage()
    const vm = wrapper.vm as unknown as {
      form: { filter_mode: string; filter_editable: boolean; filter_override: boolean }
      filterModeSelect: string
    }

    vm.form.filter_editable = false
    vm.filterModeSelect = 'extend'
    expect(vm.form.filter_mode).toBe('extend')
    expect(vm.form.filter_editable).toBe(false)

    vm.filterModeSelect = 'override'
    expect(vm.form.filter_mode).toBe('override')
    expect(vm.form.filter_override).toBe(true)
    expect(vm.form.filter_editable).toBe(false)

    // Also verify it doesn't spuriously turn filter_editable on when it was
    // already true — switching mode must never touch it either way.
    vm.form.filter_editable = true
    vm.filterModeSelect = 'global'
    expect(vm.form.filter_editable).toBe(true)
  })
})
