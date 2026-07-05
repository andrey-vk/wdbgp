import { vi, describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

const { mockGet, mockPost } = vi.hoisted(() => ({
  mockGet: vi.fn(),
  mockPost: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  default: {
    get: mockGet,
    post: mockPost,
    interceptors: { response: { use: vi.fn() } }
  }
}))

const { useBGPStatusStore } = await import('@/admin/stores/bgpStatus')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('bgpStatus store', () => {
  it('initial state is optimistically healthy', () => {
    const store = useBGPStatusStore()
    expect(store.running).toBe(true)
    expect(store.restartPending).toBe(false)
    expect(store.lastError).toBe(null)
  })

  it('fetchStatus() applies a healthy response', async () => {
    mockGet.mockResolvedValue({ data: { running: true, restart_pending: false } })
    const store = useBGPStatusStore()
    await store.fetchStatus()
    expect(mockGet).toHaveBeenCalledWith('/admin/bgp/status')
    expect(store.running).toBe(true)
    expect(store.restartPending).toBe(false)
    expect(store.lastError).toBe(null)
  })

  it('fetchStatus() applies a pending-restart response', async () => {
    mockGet.mockResolvedValue({ data: { running: true, restart_pending: true } })
    const store = useBGPStatusStore()
    await store.fetchStatus()
    expect(store.restartPending).toBe(true)
  })

  it('fetchStatus() applies a failed-speaker response', async () => {
    mockGet.mockResolvedValue({
      data: { running: false, restart_pending: false, last_error: 'bind: address already in use' },
    })
    const store = useBGPStatusStore()
    await store.fetchStatus()
    expect(store.running).toBe(false)
    expect(store.lastError).toBe('bind: address already in use')
  })

  it('fetchStatus() leaves prior status untouched on network error', async () => {
    mockGet.mockResolvedValueOnce({ data: { running: false, restart_pending: false, last_error: 'boom' } })
    const store = useBGPStatusStore()
    await store.fetchStatus()
    expect(store.running).toBe(false)

    mockGet.mockRejectedValueOnce(new Error('network error'))
    await store.fetchStatus()
    // Best-effort poll failure shouldn't clobber the last known status.
    expect(store.running).toBe(false)
    expect(store.lastError).toBe('boom')
  })

  it('reload() applies the response and reports success', async () => {
    mockPost.mockResolvedValue({ data: { running: true, restart_pending: false } })
    const store = useBGPStatusStore()
    store.restartPending = true
    const result = await store.reload()
    expect(mockPost).toHaveBeenCalledWith('/admin/bgp/reload')
    expect(result).toEqual({ ok: true, error: null })
    expect(store.restartPending).toBe(false)
    expect(store.reloading).toBe(false)
  })

  it('reload() applies the error response and reports failure', async () => {
    mockPost.mockRejectedValue({
      response: { data: { running: false, restart_pending: true, last_error: 'bind: address already in use' } },
    })
    const store = useBGPStatusStore()
    const result = await store.reload()
    expect(result).toEqual({ ok: false, error: 'bind: address already in use' })
    expect(store.running).toBe(false)
    expect(store.restartPending).toBe(true)
    expect(store.reloading).toBe(false)
  })

  it('reload() reports failure with no error text on a network error', async () => {
    mockPost.mockRejectedValue(new Error('network error'))
    const store = useBGPStatusStore()
    const result = await store.reload()
    expect(result).toEqual({ ok: false, error: null })
    expect(store.reloading).toBe(false)
  })

  it('reload() sets reloading=true while in flight', async () => {
    let resolveFn: (v: unknown) => void
    mockPost.mockReturnValue(new Promise((resolve) => { resolveFn = resolve }))
    const store = useBGPStatusStore()
    const promise = store.reload()
    expect(store.reloading).toBe(true)
    resolveFn!({ data: { running: true, restart_pending: false } })
    await promise
    expect(store.reloading).toBe(false)
  })
})
