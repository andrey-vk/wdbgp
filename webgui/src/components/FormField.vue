<script setup lang="ts">
import FieldHint from './FieldHint.vue'

defineProps<{ label: string; hint?: string; inputId: string; envVar?: string }>()
</script>

<template>
  <div class="flex flex-col gap-1">
    <div class="flex items-center gap-1.5 flex-wrap">
      <label :for="inputId" class="font-medium text-sm">{{ label }}</label>
      <slot name="tags" />
      <FieldHint
        v-if="hint"
        :hint="hint"
        :target-id="inputId"
        :env-var="envVar"
      />
    </div>
    <slot />
  </div>
</template>

