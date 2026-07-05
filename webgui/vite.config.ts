import { fileURLToPath, URL } from 'node:url';

import { PrimeVueResolver } from '@primevue/auto-import-resolver';
import tailwindcss from '@tailwindcss/vite';
import vue from '@vitejs/plugin-vue';
import Components from 'unplugin-vue-components/vite';
import { defineConfig } from 'vite';
import { resolve } from 'path';

// https://vitejs.dev/config/
export default defineConfig({
    optimizeDeps: {
        noDiscovery: true
    },
    plugins: [
        vue(),
        tailwindcss(),
        Components({
            resolvers: [PrimeVueResolver()]
        })
    ],
    resolve: {
        alias: {
            '@': fileURLToPath(new URL('./src', import.meta.url))
        }
    },
    build: {
        rollupOptions: {
            input: {
                admin: resolve(__dirname, 'admin.html'),
                user: resolve(__dirname, 'user.html'),
            }
        }
    },
    server: {
        port: 5173,
        proxy: {
            '/api': 'http://localhost:8080',
            '/healthz': 'http://localhost:8080',
            '/status': 'http://localhost:8080'
        }
    },
    css: {
        preprocessorOptions: {
            scss: {}
        }
    }
});
