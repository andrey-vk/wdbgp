import { defineStore } from 'pinia'
import { ref } from 'vue'

const STORAGE_KEY = 'wdbgp_theme'
const DARK_CLASS = 'app-dark'

export type ThemeMode = 'light' | 'dark'

function detectSystemPreference(): ThemeMode {
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export const useThemeStore = defineStore('theme', () => {
  const saved = localStorage.getItem(STORAGE_KEY) as ThemeMode | null
  // Only use localStorage if user explicitly saved it; otherwise auto-detect without saving
  const mode = ref<ThemeMode>(saved ?? detectSystemPreference())
  const isDark = ref(mode.value === 'dark')

  function applyTheme() {
    isDark.value = mode.value === 'dark'
    document.documentElement.classList.toggle(DARK_CLASS, isDark.value)
  }

  function setMode(newMode: ThemeMode) {
    mode.value = newMode
    localStorage.setItem(STORAGE_KEY, newMode)
    applyTheme()
  }

  function toggleTheme() {
    setMode(mode.value === 'light' ? 'dark' : 'light')
  }

  applyTheme()

  return { mode, isDark, setMode, toggleTheme, applyTheme }
})
