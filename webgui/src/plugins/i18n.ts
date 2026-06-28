import { createI18n } from 'vue-i18n'
import en from '@/locales/en.json'
import ru from '@/locales/ru.json'

type Locale = 'en' | 'ru'

const LANGUAGE_KEY = 'wdbgp_language'

function detectLocale(): Locale {
  const stored = localStorage.getItem(LANGUAGE_KEY)
  if (stored === 'en' || stored === 'ru') return stored
  const navLang = navigator.language?.split('-')[0]
  if (navLang === 'ru') return 'ru'
  return 'en'
}

function setLanguageCookie(locale: Locale): void {
  document.cookie = `wdbgp_language=${locale};path=/;max-age=31536000;SameSite=Lax`
}

const savedLocale = detectLocale()
setLanguageCookie(savedLocale)

const i18n = createI18n({
  legacy: false,
  locale: savedLocale,
  fallbackLocale: 'en',
  messages: { en, ru },
})

export function switchLocale(locale: Locale): void {
  i18n.global.locale.value = locale
  localStorage.setItem(LANGUAGE_KEY, locale)
  setLanguageCookie(locale)
}

export function getCurrentLocale(): Locale {
  return i18n.global.locale.value as Locale
}

export default i18n
