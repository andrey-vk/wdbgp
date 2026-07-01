import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'

const { mockGet, mockPost } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  default: {
    get: mockGet,
    post: mockPost,
    interceptors: { response: { use: vi.fn() } },
  },
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
      'bgp_status.failed_title': 'Failed to start BGP speaker — check settings',
      'bgp_status.pending_title': 'BGP settings changed',
      'bgp_status.pending_body': 'Apply pending BGP changes.',
      'bgp_status.retry': 'Retry',
      'bgp_status.apply_now': 'Apply now',
      'bgp_status.reload_success': 'BGP reloaded successfully.',
      'bgp_status.reload_error': 'BGP reload failed.',
    },
  },
})

const { useBGPStatusStore } = await import('@/admin/stores/bgpStatus')
const { default: BGPStatusBanner } = await import('@/admin/layout/BGPStatusBanner.vue')

function mountBanner() {
  return mount(BGPStatusBanner, {
    global: { plugins: [i18n, PrimeVue] },
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  mockGet.mockResolvedValue({ data: { running: true, restart_pending: false } })
})

describe('BGPStatusBanner', () => {
  it('renders nothing when healthy and no restart pending', async () => {
    const wrapper = mountBanner()
    await flushPromises()
    expect(wrapper.find('[data-testid="bgp-banner-failed"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bgp-banner-pending"]').exists()).toBe(false)
  })

  it('fetches status on mount', () => {
    mountBanner()
    expect(mockGet).toHaveBeenCalledWith('/admin/bgp/status')
  })

  it('shows the pending banner when a restart-only setting changed', async () => {
    const wrapper = mountBanner()
    const store = useBGPStatusStore()
    store.restartPending = true
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="bgp-banner-pending"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('BGP settings changed')
    expect(wrapper.find('[data-testid="bgp-banner-failed"]').exists()).toBe(false)
  })

  it('shows the failed banner (taking priority) when the speaker is down', async () => {
    const wrapper = mountBanner()
    const store = useBGPStatusStore()
    store.running = false
    store.lastError = 'bind: address already in use'
    store.restartPending = true
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-testid="bgp-banner-failed"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="bgp-banner-pending"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Failed to start BGP speaker')
    expect(wrapper.text()).toContain('bind: address already in use')
  })

  it('clicking the action button calls reload and shows a success toast', async () => {
    mockPost.mockResolvedValue({ data: { running: true, restart_pending: false } })
    const wrapper = mountBanner()
    const store = useBGPStatusStore()
    store.restartPending = true
    await wrapper.vm.$nextTick()

    await wrapper.find('[data-testid="bgp-banner-action"]').trigger('click')
    await flushPromises()

    expect(mockPost).toHaveBeenCalledWith('/admin/bgp/reload')
    expect(store.restartPending).toBe(false)
    expect(toastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'success' }))
  })

  it('clicking the action button on a failed reload shows an error toast and keeps the banner', async () => {
    mockPost.mockRejectedValue({
      response: { data: { running: false, restart_pending: true, last_error: 'still broken' } },
    })
    const wrapper = mountBanner()
    const store = useBGPStatusStore()
    store.running = false
    store.lastError = 'still broken'
    await wrapper.vm.$nextTick()

    await wrapper.find('[data-testid="bgp-banner-action"]').trigger('click')
    await flushPromises()

    expect(toastAdd).toHaveBeenCalledWith(expect.objectContaining({ severity: 'error' }))
    expect(wrapper.find('[data-testid="bgp-banner-failed"]').exists()).toBe(true)
  })
})

function flushPromises() {
  return new Promise((resolve) => setTimeout(resolve, 0))
}
