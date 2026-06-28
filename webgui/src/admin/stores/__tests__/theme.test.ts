import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

// Ensure localStorage is available (happy-dom may not provide it fully)
beforeEach(() => {
  // Create a fresh localStorage-like store
  const store = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => { store.set(key, value) },
    removeItem: (key: string) => { store.delete(key) },
    clear: () => { store.clear() },
    get length() { return store.size },
    key: (index: number) => Array.from(store.keys())[index] ?? null,
  })
  document.documentElement.classList.remove('app-dark')
  setActivePinia(createPinia())
})

// Dynamic import to avoid hoisting issues with localStorage
const { useThemeStore } = await import('@/admin/stores/theme')

describe('theme store', () => {
  it('toggleDarkMode() toggles app-dark class on document.documentElement', () => {
    const store = useThemeStore()
    // Initially depends on system preference; we can set it explicitly
    store.setMode('light')
    expect(document.documentElement.classList.contains('app-dark')).toBe(false)
    expect(store.isDark).toBe(false)
    expect(store.mode).toBe('light')

    store.toggleTheme()
    expect(document.documentElement.classList.contains('app-dark')).toBe(true)
    expect(store.isDark).toBe(true)
    expect(store.mode).toBe('dark')

    store.toggleTheme()
    expect(document.documentElement.classList.contains('app-dark')).toBe(false)
    expect(store.isDark).toBe(false)
    expect(store.mode).toBe('light')
  })

  it('initial state reads from localStorage if present', () => {
    localStorage.setItem('wdbgp_theme', 'dark')
    setActivePinia(createPinia())
    const store = useThemeStore()
    expect(store.mode).toBe('dark')
    expect(store.isDark).toBe(true)
    expect(document.documentElement.classList.contains('app-dark')).toBe(true)
  })

  it('setMode stores preference in localStorage', () => {
    const store = useThemeStore()
    store.setMode('dark')
    expect(localStorage.getItem('wdbgp_theme')).toBe('dark')
    store.setMode('light')
    expect(localStorage.getItem('wdbgp_theme')).toBe('light')
  })
})
