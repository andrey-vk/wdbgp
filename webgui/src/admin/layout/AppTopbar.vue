<script setup lang="ts">
// @ts-nocheck
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useLayout } from './composables/layout'
import { useAuthStore } from '@/admin/stores/auth'
import { getCurrentLocale } from '@/plugins/i18n'
import LanguageSwitcher from '@/components/LanguageSwitcher.vue'

const { t } = useI18n()
const { toggleMenu, toggleDarkMode, isDarkTheme } = useLayout()
const router = useRouter()
const authStore = useAuthStore()
const locale = ref(getCurrentLocale())

async function handleLogout() {
  await authStore.logout()
  router.push({ name: 'login' })
}
</script>

<template>
  <div class="layout-topbar">
    <div class="layout-topbar-logo-container">
      <button
        class="layout-menu-button layout-topbar-action"
        @click="toggleMenu"
      >
        <i class="pi pi-bars" />
      </button>
    </div>

    <div class="layout-topbar-actions">
      <button
        type="button"
        class="layout-topbar-action"
        @click="toggleDarkMode"
      >
        <i :class="['pi', { 'pi-moon': isDarkTheme, 'pi-sun': !isDarkTheme }]" />
      </button>
      <LanguageSwitcher v-model="locale" />
      <button
        type="button"
        class="layout-topbar-action"
        :title="t('topbar.logout')"
        @click="handleLogout"
      >
        <i class="pi pi-sign-out" />
      </button>
    </div>
  </div>
</template>
