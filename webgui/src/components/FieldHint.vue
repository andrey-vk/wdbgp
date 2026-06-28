<script setup lang="ts">
import { ref, inject, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Popover from 'primevue/popover'

const props = defineProps<{ hint: string; targetId: string }>()
const { t } = useI18n()
const popoverRef = ref<InstanceType<typeof Popover> | null>(null)
const activePopoverId = inject<Ref<string | null>>('activePopoverId')!
const hideActivePopover = inject<Ref<(() => void) | null>>('hideActivePopover')!

function show() {
  // Close previous popover by calling its hide function
  if (hideActivePopover.value && activePopoverId.value !== props.targetId) {
    hideActivePopover.value()
    hideActivePopover.value = null
  }
  // Open this one
  setTimeout(() => {
    const target = document.getElementById(props.targetId)
    if (target && popoverRef.value) {
      popoverRef.value.show({ currentTarget: target } as unknown as Event)
      activePopoverId.value = props.targetId
      hideActivePopover.value = () => popoverRef.value?.hide()
    }
  }, 0)
}
</script>

<template>
  <button
    type="button"
    class="hint-btn"
    @click.stop="show"
  >
    ?
  </button>
  <Popover
    ref="popoverRef"
  >
    <div class="hint-popover">
      <p>{{ t(hint) }}</p>
    </div>
  </Popover>
</template>

<style scoped>
.hint-btn {
  display: inline-flex; align-items: center; justify-content: center;
  width: 16px; height: 16px; border: 1px solid var(--p-surface-300);
  border-radius: 50%; background: transparent; color: var(--p-text-muted-color);
  font-size: 0.6rem; font-weight: 700; cursor: pointer; padding: 0; flex-shrink: 0;
}
.hint-btn:hover { background: var(--p-surface-100); color: var(--p-text-color); border-color: var(--p-surface-400); }
.hint-popover { max-width: 320px; padding: 0.25rem; }
.hint-popover p { margin: 0; font-size: 0.875rem; line-height: 1.4; }
</style>
