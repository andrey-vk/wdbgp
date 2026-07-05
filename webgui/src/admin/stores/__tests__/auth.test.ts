import { vi, describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

// Must define mock functions inside vi.mock factory (hoisted)
// or use vi.hoisted
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

// Dynamic import after mock is set up
const { useAuthStore } = await import('@/admin/stores/auth')

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('auth store', () => {
  it('initial state: isAuthenticated = false, isChecking = true', () => {
    const store = useAuthStore()
    expect(store.isAuthenticated).toBe(false)
    expect(store.isChecking).toBe(true)
  })

  it('checkAuth() calls /api/admin/me, sets authenticated on 200', async () => {
    mockGet.mockResolvedValue({ data: { authenticated: true } })
    const store = useAuthStore()
    const result = await store.checkAuth()
    expect(mockGet).toHaveBeenCalledWith('/admin/me', { skipAuthRedirect: true })
    expect(store.isAuthenticated).toBe(true)
    expect(store.isChecking).toBe(false)
    expect(result).toBe(true)
  })

  it('checkAuth() sets authenticated=false on 401', async () => {
    mockGet.mockRejectedValue({ response: { status: 401 } })
    const store = useAuthStore()
    const result = await store.checkAuth()
    expect(mockGet).toHaveBeenCalledWith('/admin/me', { skipAuthRedirect: true })
    expect(store.isAuthenticated).toBe(false)
    expect(store.isChecking).toBe(false)
    expect(result).toBe(false)
  })

  it('checkAuth() sets authenticated=false on network error', async () => {
    mockGet.mockRejectedValue(new Error('Network error'))
    const store = useAuthStore()
    const result = await store.checkAuth()
    expect(store.isAuthenticated).toBe(false)
    expect(store.isChecking).toBe(false)
    expect(result).toBe(false)
  })

  it('login(password) calls /api/admin/login, sets authenticated on success', async () => {
    mockPost.mockResolvedValue({ data: { ok: true } })
    const store = useAuthStore()
    const result = await store.login('secret')
    expect(mockPost).toHaveBeenCalledWith('/admin/login', { password: 'secret' }, { skipAuthRedirect: true })
    expect(store.isAuthenticated).toBe(true)
    expect(result).toBe(null)
  })

  it('login(password) returns error on 401', async () => {
    mockPost.mockRejectedValue({
      response: { status: 401, data: { error: 'Invalid password' } }
    })
    const store = useAuthStore()
    const result = await store.login('wrong')
    expect(result).toBe('Invalid password')
    expect(store.isAuthenticated).toBe(false)
  })

  it('login(password) returns rate limit error on 429', async () => {
    mockPost.mockRejectedValue({ response: { status: 429 } })
    const store = useAuthStore()
    const result = await store.login('any')
    expect(result).toBe('Rate limit exceeded. Please try again later.')
  })

  it('login returns network error when no response', async () => {
    mockPost.mockRejectedValue(new Error('fail'))
    const store = useAuthStore()
    const result = await store.login('any')
    expect(result).toBe('Network error. Please try again.')
  })

  it('logout() calls /api/admin/logout, clears authenticated', async () => {
    mockPost.mockResolvedValue({})
    const store = useAuthStore()
    store.isAuthenticated = true
    await store.logout()
    expect(mockPost).toHaveBeenCalledWith('/admin/logout', undefined, { skipAuthRedirect: true })
    expect(store.isAuthenticated).toBe(false)
    expect(store.isChecking).toBe(false)
  })

  it('logout() clears authenticated even on network error', async () => {
    mockPost.mockRejectedValue(new Error('fail'))
    const store = useAuthStore()
    store.isAuthenticated = true
    await store.logout()
    expect(store.isAuthenticated).toBe(false)
  })
})
