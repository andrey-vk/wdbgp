<script setup lang="ts">
import { useThemeStore, type ThemeMode } from '@/admin/stores/theme'
import SelectButton from 'primevue/selectbutton'

const themeStore = useThemeStore()

const options = [
  { label: '☀️', value: 'light' as const },
  { label: '🌙', value: 'dark' as const },
]

const model = defineModel<ThemeMode>({ required: true })
</script>

<template>
  <SelectButton
    v-model="model"
    :options="options"
    option-label="label"
    option-value="value"
    :allow-empty="false"
    size="small"
    @change="themeStore.setMode($event.value as ThemeMode)"
  />
</template>
