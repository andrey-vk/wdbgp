<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import FormField from './FormField.vue'
import Checkbox from 'primevue/checkbox'

const { t } = useI18n()

const props = defineProps<{
  label?: string
  hint?: string
  inputId: string
  defaultValue?: string
  modelValue?: string | number | boolean | null
  type?: 'text' | 'number' | 'bool' | 'select'
  disabled?: boolean
  envOverride?: boolean
  restart?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string | number | boolean | null]
}>()

// useDefault: checkbox checked = inherit default (don't save to DB)
// unchecked = user override (save custom value)
const useDefault = ref(props.modelValue == null || props.modelValue === '' || String(props.modelValue) === props.defaultValue)

// Sync checkbox when external modelValue changes
watch(() => props.modelValue, (val) => {
  if (val == null || val === '' || String(val) === props.defaultValue) {
    useDefault.value = true
  } else {
    useDefault.value = false
  }
})

// Internal value for the field
const fieldValue = computed({
  get() {
    if (useDefault.value) {
      return props.defaultValue ?? ''
    }
    return props.modelValue ?? ''
  },
  set(val) {
    if (!useDefault.value) {
      emit('update:modelValue', val)
    }
  }
})

function toggleDefault(checked: boolean) {
  if (checked) {
    // User checked "Use default" → clear the override
    emit('update:modelValue', null)
  } else {
    // User unchecked → enter custom mode, prefill with default
    emit('update:modelValue', props.defaultValue ?? '')
  }
}
</script>

<template>
  <FormField
    :label="label"
    :hint="hint"
    :input-id="inputId"
  >
    <template #tags>
      <slot name="tags" />
    </template>
    <div class="flex items-center gap-3">
      <div class="flex-1">
        <slot
          :value="fieldValue"
          :disabled="disabled || useDefault"
          :input-id="inputId"
        />
      </div>
      <div
        v-if="defaultValue != null"
        class="flex items-center gap-1.5 shrink-0"
      >
        <Checkbox
          :input-id="inputId + '-default'"
          :model-value="useDefault"
          :binary="true"
          @update:model-value="toggleDefault"
        />
        <label
          :for="inputId + '-default'"
          class="text-sm text-gray-500 dark:text-gray-400 cursor-pointer whitespace-nowrap"
        >{{ t('settings.use_default') }}</label>
      </div>
    </div>
  </FormField>
</template>
