import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'

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
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('primevue/useconfirm', () => ({
  useConfirm: () => ({ require: vi.fn() }),
}))

const toastAdd = vi.fn()
vi.mock('primevue/usetoast', () => ({
  useToast: () => ({ add: toastAdd }),
}))

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  messages: {
    en: {
      'modes.error_name': 'Name is required',
      'modes.feeds_save_failed': 'Failed to save feed assignments',
      'modes.saved': 'Mode saved',
    },
  },
})

function stubPrimeVueComponents() {
  return {
    InputText: { template: '<input class="stub-input" />', inheritAttrs: false },
    ToggleSwitch: { template: '<input class="stub-toggle" type="checkbox" />', inheritAttrs: false },
    Checkbox: { template: '<input class="stub-checkbox" type="checkbox" />', inheritAttrs: false },
    Button: { props: ['label'], template: '<button class="stub-btn">{{ label }}<slot /></button>' },
    Tag: { template: '<span class="stub-tag"><slot /></span>' },
  }
}

async function mountModesPage() {
  const ModesPage = (await import('../ModesPage.vue')).default
  const wrapper = mount(ModesPage, {
    global: {
      plugins: [i18n, PrimeVue],
      stubs: stubPrimeVueComponents(),
    },
  })
  await wrapper.vm.$nextTick()
  await new Promise(resolve => setTimeout(resolve, 0))
  return { wrapper, ModesPage }
}

type ModesPageVM = InstanceType<Awaited<ReturnType<typeof mountModesPage>>['ModesPage']>

beforeEach(() => {
  mockGet.mockReset()
  mockPost.mockReset()
  mockPut.mockReset()
  mockDelete.mockReset()
  toastAdd.mockReset()
})

describe('ModesPage', () => {
  it('resyncs the mode list and exits edit mode even when the feed-assignment save fails, so a retry updates instead of duplicating', async () => {
    mockGet.mockImplementation((url: string) => {
      if (url === '/admin/modes') {
        return Promise.resolve({ data: { modes: [] } })
      }
      if (url === '/admin/modes/42/feeds') {
        return Promise.resolve({ data: { feeds: [] } })
      }
      return Promise.resolve({ data: {} })
    })

    const { wrapper } = await mountModesPage()
    const vm = wrapper.vm as ModesPageVM

    vm.startNew()
    vm.form.name = 'new-mode'

    mockPost.mockImplementation((url: string) => {
      if (url === '/admin/modes') {
        return Promise.resolve({ data: { id: 42, name: 'new-mode', enabled: true, feed_count: 0 } })
      }
      return Promise.resolve({ data: {} })
    })
    mockPut.mockImplementation((url: string) => {
      if (url === '/admin/modes/42/feeds') {
        return Promise.reject(new Error('network error'))
      }
      return Promise.resolve({ data: {} })
    })

    // After the create, the mode-list GET must reflect the now-persisted
    // mode so the sidebar doesn't leave it invisible.
    mockGet.mockImplementation((url: string) => {
      if (url === '/admin/modes') {
        return Promise.resolve({ data: { modes: [{ id: 42, name: 'new-mode', enabled: true, feed_count: 0 }] } })
      }
      if (url === '/admin/modes/42/feeds') {
        return Promise.resolve({ data: { feeds: [] } })
      }
      return Promise.resolve({ data: {} })
    })

    await vm.handleSave()

    expect(toastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'error' }))
    // The failed feed assignment must not leave the newly-created mode
    // invisible: selected/editMode must resync to the persisted mode.
    expect(vm.selected?.id).toBe(42)
    expect(vm.editMode).toBe(false)

    // Retrying now must PUT (update the existing mode) rather than
    // POST (create a duplicate) — proving selected.value was set correctly.
    mockPost.mockClear()
    mockPut.mockClear()
    mockPut.mockResolvedValue({ data: {} })
    await vm.handleSave()

    expect(mockPost).not.toHaveBeenCalledWith('/admin/modes', expect.anything())
    expect(mockPut).toHaveBeenCalledWith('/admin/modes/42', expect.anything())
  })

  it('saves feed assignments with include/exclude roles', async () => {
    mockGet.mockImplementation((url: string) => {
      if (url === '/admin/modes') {
        return Promise.resolve({ data: { modes: [] } })
      }
      if (url === '/admin/modes/7/feeds') {
        return Promise.resolve({ data: { feeds: [] } })
      }
      if (url === '/admin/feeds') {
        return Promise.resolve({ data: { feeds: [
          { id: 1, name: 'inc', url: 'u1', enabled: true, adapter_name: 'a' },
          { id: 2, name: 'exc', url: 'u2', enabled: true, adapter_name: 'a' },
        ] } })
      }
      return Promise.resolve({ data: {} })
    })

    const { wrapper } = await mountModesPage()
    const vm = wrapper.vm as ModesPageVM

    vm.startNew()
    vm.form.name = 'roles-mode'
    vm.assignedFeedIds.push(1, 2)
    vm.excludedFeedIds.push(2)

    mockPost.mockResolvedValue({ data: { id: 7, name: 'roles-mode', enabled: true, feed_count: 2 } })
    mockPut.mockResolvedValue({ data: {} })

    await vm.handleSave()

    expect(mockPut).toHaveBeenCalledWith('/admin/modes/7/feeds', {
      feeds: [
        { id: 1, exclude: false },
        { id: 2, exclude: true },
      ],
    })
  })
})
