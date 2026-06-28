<script setup lang="ts">
import FieldHint from './FieldHint.vue'

defineProps<{ label: string; hint?: string; inputId: string }>()
</script>

<template>
  <div class="form-field">
    <div class="label-row">
      <label :for="inputId">{{ label }}</label>
      <slot name="tags" />
      <FieldHint
        v-if="hint"
        :hint="hint"
        :target-id="inputId"
      />
    </div>
    <slot />
  </div>
</template>

<style scoped>
.form-field { display: flex; flex-direction: column; gap: 0.25rem; }
.label-row { display: flex; align-items: center; gap: 0.375rem; flex-wrap: wrap; }
.label-row label { font-weight: 500; font-size: 0.875rem; }
</style>
