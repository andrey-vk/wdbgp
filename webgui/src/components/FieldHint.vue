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
    class="inline-flex items-center justify-center w-4 h-4 border border-surface-300 rounded-full bg-transparent text-muted-color text-[0.6rem] font-bold cursor-pointer p-0 shrink-0 hover:bg-surface-100 hover:text-color hover:border-surface-400"
    @click.stop="show"
  >
    ?
  </button>
  <Popover
    ref="popoverRef"
  >
    <div class="max-w-[320px] p-1">
      <p class="m-0 text-sm leading-relaxed">{{ t(hint) }}</p>
    </div>
  </Popover>
</template>

