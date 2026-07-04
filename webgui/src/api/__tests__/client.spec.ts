import { describe, it, expect, beforeEach } from 'vitest'
import apiClient from '../client'
import type { AxiosAdapter } from 'axios'

function mockAdapterFor(status: number): AxiosAdapter {
  return async (config) => {
    const err = new Error('Request failed') as Error & { isAxiosError: boolean; config: unknown; response: unknown }
    err.isAxiosError = true
    err.config = config
    err.response = { status, data: {}, statusText: '', headers: {}, config }
    throw err
  }
}

describe('apiClient 401 redirect interceptor', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'location', {
      value: { href: '', pathname: '/admin/users' },
      writable: true,
      configurable: true,
    })
  })

  it('redirects to the admin login page on a 401 from a generic admin endpoint', async () => {
    apiClient.defaults.adapter = mockAdapterFor(401)
    await apiClient.get('/admin/users').catch(() => {})
    expect(window.location.href).toBe('/admin/auth/login')
  })

  it('does not redirect for /admin/me — its 401 is the expected "not logged in" signal the session probe itself handles', async () => {
    apiClient.defaults.adapter = mockAdapterFor(401)
    await apiClient.get('/admin/me').catch(() => {})
    expect(window.location.href).toBe('')
  })

  it('does not redirect for /admin/login — a failed login attempt must stay on the form with its own error message', async () => {
    apiClient.defaults.adapter = mockAdapterFor(401)
    await apiClient.post('/admin/login', { password: 'wrong' }).catch(() => {})
    expect(window.location.href).toBe('')
  })

  it('does not redirect for /user/ endpoints — a regular user session expiring must not send them to the admin login', async () => {
    apiClient.defaults.adapter = mockAdapterFor(401)
    await apiClient.get('/user/me').catch(() => {})
    expect(window.location.href).toBe('')
  })

  it('does not redirect when already on the login page', async () => {
    window.location.pathname = '/admin/auth/login'
    apiClient.defaults.adapter = mockAdapterFor(401)
    await apiClient.get('/admin/users').catch(() => {})
    expect(window.location.href).toBe('')
  })

  it('does not redirect on a non-401 error', async () => {
    apiClient.defaults.adapter = mockAdapterFor(500)
    await apiClient.get('/admin/users').catch(() => {})
    expect(window.location.href).toBe('')
  })

  it('still rejects the promise so callers keep their own error handling', async () => {
    apiClient.defaults.adapter = mockAdapterFor(401)
    await expect(apiClient.get('/admin/users')).rejects.toBeTruthy()
  })
})
