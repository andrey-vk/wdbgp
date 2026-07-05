<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'

const { t } = useI18n()

defineProps<{
  title?: string
  message?: string
}>()

function reload() {
  window.location.reload()
}
</script>

<template>
  <div class="flex items-center justify-center min-h-[50vh]">
    <div class="text-center max-w-md">
      <i class="pi pi-exclamation-triangle text-4xl text-yellow-500 mb-4 block" />
      <h2 class="text-xl font-semibold mb-2">{{ title || t('error.generic_title') }}</h2>
      <p class="text-muted-color mb-4">{{ message || t('error.generic_message') }}</p>
      <Button icon="pi pi-refresh" :label="t('error.reload')" severity="secondary" @click="reload" />
    </div>
  </div>
</template>
