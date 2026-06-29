import { defineStore } from 'pinia'
import { ref } from 'vue'
import apiClient from '@/api/client'

let _checkPromise: Promise<boolean> | null = null

export const useAuthStore = defineStore('auth', () => {
  const isAuthenticated = ref(false)
  const isChecking = ref(true) // true while initial session check is in progress

  async function login(password: string): Promise<string | null> {
    try {
      const response = await apiClient.post('/admin/login', { password })
      if (response.data.ok) {
        isAuthenticated.value = true
        return null // success, no error
      }
      return response.data.error || 'Login failed'
    } catch (err: unknown) {
      const e = err as { response?: { status?: number; data?: { error?: string } } }
      if (e.response?.status === 401) {
        return e.response.data?.error || 'Invalid password'
      }
      if (e.response?.status === 429) {
        return 'Rate limit exceeded. Please try again later.'
      }
      return 'Network error. Please try again.'
    }
  }

  async function checkAuth(): Promise<boolean> {
    if (_checkPromise) return _checkPromise

    isChecking.value = true
    _checkPromise = (async () => {
      try {
        const response = await apiClient.get('/admin/me')
        isAuthenticated.value = response.data.authenticated === true
        return isAuthenticated.value
      } catch {
        isAuthenticated.value = false
        return false
      } finally {
        isChecking.value = false
        _checkPromise = null
      }
    })()
    return _checkPromise
  }

  async function logout(): Promise<void> {
    try {
      await apiClient.post('/admin/logout')
    } catch {
      // ignore logout errors
    }
    isAuthenticated.value = false
    isChecking.value = false
  }

  return { isAuthenticated, isChecking, login, checkAuth, logout }
})
