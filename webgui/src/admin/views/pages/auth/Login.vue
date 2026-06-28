<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/admin/stores/auth'
import { getCurrentLocale } from '@/plugins/i18n'
import Password from 'primevue/password'
import Button from 'primevue/button'
import Message from 'primevue/message'
import LanguageSwitcher from '@/components/LanguageSwitcher.vue'
import { useThemeStore } from '@/admin/stores/theme'
import ThemeSwitcher from '@/admin/components/ThemeSwitcher.vue'
import FormField from '@/components/FormField.vue'

const { t } = useI18n()
const router = useRouter()
const authStore = useAuthStore()

const password = ref('')
const error = ref('')
const loading = ref(false)
const locale = ref(getCurrentLocale())
const themeStore = useThemeStore()
const themeMode = ref(themeStore.mode)

function translateError(err: string): string {
  if (err.includes('Invalid password')) return t('login.invalid_password')
  if (err.includes('Rate limit')) return t('login.rate_limit')
  if (err.includes('Network error')) return t('login.network_error')
  if (err.includes('Invalid request')) return t('login.bad_request')
  return err
}

async function handleSubmit() {
  error.value = ''
  loading.value = true
  const err = await authStore.login(password.value)
  loading.value = false
  if (err) {
    error.value = translateError(err)
  } else {
    router.push({ name: 'dashboard' })
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-header">
        <h1>{{ t('app.title') }}</h1>
        <p>{{ t('app.subtitle') }}</p>
      </div>

      <form
        class="login-form"
        @submit.prevent="handleSubmit"
      >
        <Message
          v-if="error"
          severity="error"
          :closable="false"
        >
          {{ error }}
        </Message>

        <FormField
          :label="t('login.password')"
          :hint="'login.password_hint'"
          input-id="password"
        >
          <Password
            id="password"
            v-model="password"
            :feedback="false"
            toggle-mask
            fluid
            autofocus
          />
        </FormField>

        <Button
          type="submit"
          :label="t('login.submit')"
          icon="pi pi-sign-in"
          :loading="loading"
          fluid
          severity="primary"
        />
      </form>

      <div class="login-footer">
        <ThemeSwitcher v-model="themeMode" />
        <LanguageSwitcher v-model="locale" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  background: var(--p-surface-ground);
  padding: 1rem;
}
.login-card {
  background: var(--p-surface-card);
  border-radius: var(--p-border-radius-lg);
  box-shadow: var(--p-shadow-md);
  padding: 2rem;
  width: 100%;
  max-width: 400px;
}
.login-header {
  text-align: center;
  margin-bottom: 2rem;
}
.login-header h1 {
  font-size: 2rem;
  font-weight: 700;
  color: var(--p-primary-color);
  margin: 0 0 0.25rem;
}
.login-header p {
  color: var(--p-text-muted-color);
  margin: 0;
}
.login-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.login-footer {
  display: flex;
  justify-content: center;
  gap: 0.75rem;
  margin-top: 1.5rem;
}
</style>
