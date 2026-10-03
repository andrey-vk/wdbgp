<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import type { BlastRadiusPreview } from '@/types/blast-radius'
import { changedUsers } from '@/types/blast-radius'

const { t } = useI18n()

const props = defineProps<{
  visible: boolean
  preview: BlastRadiusPreview
  loading?: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  apply: []
  cancel: []
}>()

// Only the users whose count actually moved — the backend's affected-user
// set is membership-based (e.g. "everyone on this mode"), so most entries
// commonly show no change at all and would just be noise here.
const rows = computed(() => changedUsers(props.preview))

function close() {
  emit('update:visible', false)
  emit('cancel')
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :header="t('blast_radius.title')"
    :style="{ width: '34rem' }"
    @update:visible="(v: boolean) => { if (!v) close() }"
  >
    <p class="mb-3 font-semibold">{{ t('blast_radius.count', { count: rows.length }) }}</p>
    <div class="max-h-[50vh] overflow-y-auto border border-gray-200 dark:border-gray-700 rounded">
      <div
        v-for="row in rows"
        :key="row.user_id"
        class="flex items-center gap-3 px-3 py-1.5 border-b border-gray-100 dark:border-gray-800 last:border-b-0 text-sm"
      >
        <span class="flex-1 truncate">{{ row.name }}</span>
        <Tag v-if="row.lost_routes" severity="danger" :value="t('blast_radius.loses_routes')" />
        <span class="shrink-0 tabular-nums text-gray-500 dark:text-gray-400">
          {{ t('blast_radius.v4') }}:
          <span class="text-gray-700 dark:text-gray-300">{{ row.before_v4 }}</span>
          <i class="pi pi-arrow-right mx-1 text-xs" />
          <span class="font-semibold">{{ row.after_v4 }}</span>
        </span>
        <span class="shrink-0 tabular-nums text-gray-500 dark:text-gray-400">
          {{ t('blast_radius.v6') }}:
          <span class="text-gray-700 dark:text-gray-300">{{ row.before_v6 }}</span>
          <i class="pi pi-arrow-right mx-1 text-xs" />
          <span class="font-semibold">{{ row.after_v6 }}</span>
        </span>
      </div>
    </div>
    <p class="mt-3 text-sm text-gray-500 dark:text-gray-400">
      {{ t('blast_radius.total', { v4: preview.total_delta_v4, v6: preview.total_delta_v6 }) }}
    </p>
    <template #footer>
      <Button :label="t('dialog.no')" severity="secondary" text @click="close" />
      <Button :label="t('blast_radius.apply')" severity="danger" :loading="loading" @click="emit('apply')" />
    </template>
  </Dialog>
</template>
