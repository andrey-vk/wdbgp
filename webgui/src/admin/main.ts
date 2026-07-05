import { createApp } from 'vue';
import { createPinia } from 'pinia';
import App from './App.vue';
import router from './router';
import i18n from '@/plugins/i18n';
import { useThemeStore } from './stores/theme';

import Aura from '@primeuix/themes/aura';
import PrimeVue from 'primevue/config';
import ConfirmationService from 'primevue/confirmationservice';
import ToastService from 'primevue/toastservice';

import { plugin, defaultConfig } from '@formkit/vue';
import formkitConfig from '@/formkit.config';

import '@/assets/tailwind.css';
import '@/assets/styles.scss';

const app = createApp(App);

const pinia = createPinia();
app.use(pinia);
app.use(router);
const themeStore = useThemeStore();
themeStore.applyTheme();
app.use(i18n);
app.use(PrimeVue, {
    theme: {
        preset: Aura,
        options: {
            darkModeSelector: '.app-dark',
            cssLayer: {
                name: 'primevue',
                order: 'theme, base, primevue',
            }
        }
    }
});
app.use(ToastService);
app.use(ConfirmationService);
app.use(plugin, defaultConfig(formkitConfig));

app.mount('#app');
