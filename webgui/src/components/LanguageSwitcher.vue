<script setup lang="ts">
import { switchLocale, getCurrentLocale } from '@/plugins/i18n'
import SelectButton from 'primevue/selectbutton'

type Locale = ReturnType<typeof getCurrentLocale>

const options = [
  { label: 'EN', value: 'en' as const },
  { label: 'RU', value: 'ru' as const },
]

const model = defineModel<Locale>({ required: true })
</script>

<template>
  <SelectButton
    v-model="model"
    :options="options"
    option-label="label"
    option-value="value"
    :allow-empty="false"
    size="small"
    @change="switchLocale($event.value as Locale)"
  />
</template>
