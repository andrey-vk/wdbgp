<script setup lang="ts">
import { ref, provide, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// Global popover state — only one at a time
const activePopoverId = ref<string | null>(null)
const hideActivePopover = ref<(() => void) | null>(null)
provide('activePopoverId', activePopoverId)
provide('hideActivePopover', hideActivePopover)

function updateTitle() {
  document.title = t('app.title_full')
}

watch(locale, updateTitle, { immediate: true })
</script>

<template>
  <router-view />
</template>
