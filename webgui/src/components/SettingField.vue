<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import FormField from './FormField.vue'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import ToggleSwitch from 'primevue/toggleswitch'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import type { SettingMeta } from '@/admin/settingsMeta'

const { t } = useI18n()

const props = defineProps<{
  fieldKey: string
  meta: SettingMeta
  value: boolean | number | string | null
  defaultValue: boolean | number | string
  envOverride: boolean
  restart?: boolean
  envVar?: string
}>()

const emit = defineEmits<{
  'update:value': [value: boolean | number | string | null]
}>()

// editing tracks whether the default was clicked into editing mode (case 1 → editing)
const editing = ref(false)

function formatDisplayValue(val: boolean | number | string, type: SettingMeta['type']): string {
  switch (type) {
    case 'bool':
      return val ? t('settings.on') : t('settings.off')
    case 'number':
      return String(val)
    case 'select': {
      const opt = props.meta.options
      if (opt && typeof val === 'string' && opt[val]) {
        return t(opt[val])
      }
      return String(val)
    }
    default:
      return String(val)
  }
}

function onChange(val: boolean | number | string | undefined) {
  if (val !== undefined) {
    emit('update:value', val)
  }
}

function revert() {
  editing.value = false
  emit('update:value', null)
}

const selectOptions = computed(() => {
  if (!props.meta.options) return []
  return Object.entries(props.meta.options).map(([value, labelKey]) => ({
    label: t(labelKey),
    value,
  }))
})
</script>

<template>
  <FormField
    :label="t(meta.label)"
    :hint="meta.hint"
    :input-id="fieldKey"
  >
    <template #tags>
      <Tag
        v-if="envOverride"
        severity="warn"
        value="ENV"
        :title="t('settings.env_override_hint')"
      />
      <Tag
        v-if="restart"
        severity="info"
        :value="t('settings.requires_restart')"
      />
    </template>

    <!-- Case 2: env override — readonly text -->
    <div
      v-if="envOverride"
      class="text-sm"
    >
      {{ formatDisplayValue(value ?? defaultValue, meta.type) }}
    </div>

    <!-- Case 1: default — clickable text -->
    <div
      v-else-if="!editing && value == null"
      class="setting-default border-b border-dashed border-[var(--p-text-color)] cursor-pointer opacity-70 hover:opacity-100"
      :title="t('settings.click_to_override')"
      @click="editing = true"
    >
      {{ formatDisplayValue(defaultValue, meta.type) }}
    </div>

    <!-- Case 1 (editing) or Case 3: input with revert -->
    <div
      v-else
      class="flex items-center gap-2"
    >
      <div class="flex-1">
        <ToggleSwitch
          v-if="meta.type === 'bool'"
          :model-value="value as boolean"
          :input-id="fieldKey"
          @update:model-value="onChange"
        />
        <InputNumber
          v-else-if="meta.type === 'number'"
          :model-value="value as number"
          :input-id="fieldKey"
          fluid
          @update:model-value="onChange"
        />
        <Select
          v-else-if="meta.type === 'select'"
          :model-value="value as string"
          :options="selectOptions"
          option-label="label"
          option-value="value"
          :input-id="fieldKey"
          fluid
          @update:model-value="onChange"
        />
        <InputText
          v-else
          :model-value="value as string"
          :input-id="fieldKey"
          fluid
          @update:model-value="onChange"
        />
      </div>
      <Button
        icon="pi pi-undo"
        severity="secondary"
        text
        rounded
        :title="t('settings.revert_default')"
        @click="revert"
      />
    </div>
  </FormField>
</template>

