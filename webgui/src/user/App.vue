<script setup lang="ts">
import { ref, provide } from 'vue'

const activePopoverId = ref<string | null>(null)
const hideActivePopover = ref<(() => void) | null>(null)
provide('activePopoverId', activePopoverId)
provide('hideActivePopover', hideActivePopover)
</script>

<template>
  <router-view />
</template>
