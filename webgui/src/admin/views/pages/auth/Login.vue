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
  <div class="flex items-center justify-center min-h-screen bg-surface-50 dark:bg-surface-950 p-4">
    <div class="bg-surface-0 dark:bg-surface-900 rounded-xl shadow-md p-8 w-full max-w-[400px]">
      <div class="text-center mb-8">
        <h1 class="text-[2rem] font-bold text-primary m-0 mb-1">{{ t('app.title') }}</h1>
        <p class="text-muted-color m-0">{{ t('app.subtitle') }}</p>
      </div>

      <form
        class="flex flex-col gap-4"
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

      <div class="flex justify-center gap-3 mt-6">
        <ThemeSwitcher v-model="themeMode" />
        <LanguageSwitcher v-model="locale" />
      </div>
    </div>
  </div>
</template>

