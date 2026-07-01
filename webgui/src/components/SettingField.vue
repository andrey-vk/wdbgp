<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import FormField from './FormField.vue'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import ToggleSwitch from 'primevue/toggleswitch'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import Textarea from 'primevue/textarea'
import Select from 'primevue/select'
import type { SettingMeta } from '@/admin/settingsMeta'

const { t } = useI18n()

const props = defineProps<{
  fieldKey: string
  meta: SettingMeta
  value: string | number | boolean | null
  defaultValue: string | number | boolean
  envOverride: boolean
  restart?: boolean
  envVar?: string
}>()

const emit = defineEmits<{
  'update:value': [value: string | number | boolean | null]
}>()

// editing tracks whether the default was clicked into editing mode (case 1 → editing)
const editing = ref(false)

function startEditing() {
  editing.value = true
  if (props.value == null && props.meta.type !== 'password') {
    emit('update:value', props.defaultValue)
  }
}

function formatDisplayValue(val: string | number | boolean, type: SettingMeta['type']): string {
  switch (type) {
    case 'bool':
      return val ? t('settings.on') : t('settings.off')
    case 'number':
      return String(val)
    case 'string':
    case 'textarea':
      if (val) return String(val)
      return '\u2039' + t('settings.empty') + '\u203a'
    case 'password':
      return val ? t('settings.password_set') : t('settings.password_not_set')
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

function onChange(val: string | number | boolean | undefined) {
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
    :env-var="meta.envVar"
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
      :id="fieldKey"
      class="text-sm"
      data-testid="setting-env"
    >
      {{ meta.type === 'password' ? t('settings.password_set') : formatDisplayValue(value ?? defaultValue, meta.type) }}
    </div>

    <!-- Case 1: default — clickable text -->
    <div
      v-else-if="!editing && value == null"
      :id="fieldKey"
      data-testid="setting-default"
    >
      <span
        class="underline decoration-dotted underline-offset-4 cursor-pointer opacity-70 hover:opacity-100"
        :title="t('settings.click_to_override')"
        @click="startEditing"
      >{{ formatDisplayValue(defaultValue, meta.type) }}</span>
    </div>

    <!-- Case 1 (editing) or Case 3: input with revert -->
    <div
      v-else
      class="flex items-center gap-2"
      data-testid="setting-editing"
    >
      <div class="flex-1">
        <ToggleSwitch
          v-if="meta.type === 'bool'"
          :model-value="(value ?? defaultValue) as boolean"
          :input-id="fieldKey"
          @update:model-value="onChange"
        />
        <InputNumber
          v-else-if="meta.type === 'number'"
          :model-value="(value ?? defaultValue) as number"
          :input-id="fieldKey"
          fluid
          @update:model-value="onChange"
        />
        <Select
          v-else-if="meta.type === 'select'"
          :model-value="(value ?? defaultValue) as string"
          :options="selectOptions"
          option-label="label"
          option-value="value"
          :input-id="fieldKey"
          fluid
          @update:model-value="onChange"
        />
        <Textarea
          v-else-if="meta.type === 'textarea'"
          :model-value="(value ?? defaultValue) as string"
          :input-id="fieldKey"
          rows="5"
          fluid
          @update:model-value="onChange"
        />
        <template v-else-if="meta.type === 'password'">
          <Password
            :model-value="(value ?? '') as string"
            :input-id="fieldKey"
            fluid
            :feedback="false"
            @update:model-value="onChange"
          />
          <p v-if="!value" class="text-xs text-muted-color mt-1">{{ t('settings.password_input_hint') }}</p>
        </template>
        <InputText
          v-else
          :model-value="(value ?? defaultValue) as string"
          :input-id="fieldKey"
          fluid
          @update:model-value="onChange"
        />
      </div>
      <Button
        v-if="meta.type !== 'password'"
        icon="pi pi-undo"
        severity="secondary"
        text
        rounded
        data-testid="setting-revert"
        :title="t('settings.revert_default')"
        @click="revert"
      />
    </div>
  </FormField>
</template>

