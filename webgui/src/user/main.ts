import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import PrimeVue from 'primevue/config'
import ToastService from 'primevue/toastservice'
import Aura from '@primeuix/themes/aura'
import App from './App.vue'
import router from './router'
import en from '@/locales/en.json'
import ru from '@/locales/ru.json'

import 'primeicons/primeicons.css'
import '@/assets/tailwind.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(PrimeVue, {
  theme: {
    preset: Aura,
    options: {
      darkModeSelector: '.app-dark',
      cssLayer: {
        name: 'primevue',
        order: 'theme, base, primevue',
      },
    },
  },
})
app.use(ToastService)
app.use(createI18n({
  legacy: false,
  locale: navigator.language.startsWith('ru') ? 'ru' : 'en',
  fallbackLocale: 'en',
  messages: { en, ru },
}))
app.mount('#app')
