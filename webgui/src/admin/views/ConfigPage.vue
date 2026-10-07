<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import axios from 'axios'
import apiClient from '@/api/client'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import ConfigDiffSummary from '@/admin/components/ConfigDiffSummary.vue'
import type {
  ConfigSnapshot,
  ConfigDiff,
  ConfigImportPreviewResponse,
  ConfigImportResponse,
} from '@/types/config-snapshot'

const { t } = useI18n()
const toast = useToast()

function readJSONFile(file: File): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      try {
        resolve(JSON.parse(String(reader.result)))
      } catch {
        reject(new Error('invalid json'))
      }
    }
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsText(file)
  })
}

// =============================================================================
// Export
// =============================================================================

const exporting = ref(false)
async function handleExport() {
  exporting.value = true
  try {
    const resp = await apiClient.get<ConfigSnapshot>('/admin/config/export')
    const text = JSON.stringify(resp.data, null, 2)
    const blob = new Blob([text], { type: 'application/json' })
    const link = document.createElement('a')
    link.href = URL.createObjectURL(blob)
    link.download = `wdbgp-config-${new Date().toISOString().slice(0, 10)}.json`
    link.click()
    URL.revokeObjectURL(link.href)
  } catch {
    toast.add({ severity: 'error', summary: t('config.export_failed'), life: 3000 })
  } finally {
    exporting.value = false
  }
}

// =============================================================================
// Import — always previews first (a dry-run diff against this instance's
// current configuration); apply only happens on explicit confirmation, and
// only after the server re-confirms the previewed snapshot hasn't changed
// since (the digest check) — see internal/web/handlers_api_config.go.
// =============================================================================

const importFileInput = ref<HTMLInputElement>()
const importing = ref(false)
const applying = ref(false)
const showImportDialog = ref(false)
const importDiff = ref<ConfigDiff | null>(null)
const importDigest = ref('')
const pendingSnapshot = ref<ConfigSnapshot | null>(null)

function hasAnyChange(diff: ConfigDiff): boolean {
  return (
    diff.global_filters_changed ||
    diff.modes.added.length > 0 || diff.modes.changed.length > 0 || diff.modes.removed.length > 0 ||
    diff.users.added.length > 0 || diff.users.changed.length > 0 || diff.users.removed.length > 0
  )
}

async function onImportFileChange(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  importing.value = true
  try {
    const parsed = await readJSONFile(file)
    const resp = await apiClient.post<ConfigImportPreviewResponse>('/admin/config/import/preview', { snapshot: parsed })
    pendingSnapshot.value = parsed as ConfigSnapshot
    importDiff.value = resp.data.diff
    importDigest.value = resp.data.digest
    if (!hasAnyChange(importDiff.value)) {
      toast.add({ severity: 'info', summary: t('config.import_no_changes'), life: 3000 })
      pendingSnapshot.value = null
    } else {
      showImportDialog.value = true
    }
  } catch (err: unknown) {
    if (err instanceof Error && err.message === 'invalid json') {
      toast.add({ severity: 'error', summary: t('config.import_invalid_file'), life: 4000 })
    } else {
      toast.add({ severity: 'error', summary: t('config.import_preview_failed'), life: 4000 })
    }
  } finally {
    importing.value = false
    if (importFileInput.value) importFileInput.value.value = ''
  }
}

async function confirmImport() {
  if (!pendingSnapshot.value) return
  applying.value = true
  try {
    const resp = await apiClient.post<ConfigImportResponse>('/admin/config/import', {
      snapshot: pendingSnapshot.value,
      digest: importDigest.value,
    })
    const r = resp.data.result
    toast.add({
      severity: 'success',
      summary: t('config.import_done', {
        usersCreated: r.users_created?.length ?? 0,
        usersUpdated: r.users_updated?.length ?? 0,
        modesCreated: r.modes_created?.length ?? 0,
        modesUpdated: r.modes_updated?.length ?? 0,
      }),
      life: 5000,
    })
    if (resp.data.global_filters_applied) {
      toast.add({ severity: 'info', summary: t('config.import_filters_applied'), life: 4000 })
    }
    showImportDialog.value = false
    pendingSnapshot.value = null
  } catch (e: unknown) {
    if (axios.isAxiosError(e) && e.response?.status === 409) {
      toast.add({ severity: 'warn', summary: t('config.import_stale'), life: 5000 })
    } else {
      toast.add({ severity: 'error', summary: t('config.import_failed'), life: 4000 })
    }
    showImportDialog.value = false
    pendingSnapshot.value = null
  } finally {
    applying.value = false
  }
}

// =============================================================================
// Compare — any two exports, directly against each other. Purely
// informational: nothing here is ever applied to this instance.
// =============================================================================

const compareAInput = ref<HTMLInputElement>()
const compareBInput = ref<HTMLInputElement>()
const snapshotA = ref<ConfigSnapshot | null>(null)
const snapshotB = ref<ConfigSnapshot | null>(null)
const comparing = ref(false)
const compareDiff = ref<ConfigDiff | null>(null)

async function onCompareFileChange(which: 'a' | 'b', e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    const parsed = (await readJSONFile(file)) as ConfigSnapshot
    if (which === 'a') snapshotA.value = parsed
    else snapshotB.value = parsed
    compareDiff.value = null
  } catch {
    toast.add({ severity: 'error', summary: t('config.compare_invalid_file'), life: 4000 })
  }
}

async function handleCompare() {
  if (!snapshotA.value || !snapshotB.value) return
  comparing.value = true
  try {
    const resp = await apiClient.post<{ diff: ConfigDiff }>('/admin/config/diff', { a: snapshotA.value, b: snapshotB.value })
    compareDiff.value = resp.data.diff
  } catch {
    toast.add({ severity: 'error', summary: t('config.compare_failed'), life: 4000 })
  } finally {
    comparing.value = false
  }
}
</script>

<template>
  <div class="max-w-[900px] min-h-[70vh] px-5 py-4">
    <h2 class="font-semibold text-lg mb-4">{{ t('config.title') }}</h2>

    <section class="mb-8 p-4 border border-gray-200 dark:border-gray-700 rounded-lg">
      <h3 class="font-semibold mb-1">{{ t('config.export_section') }}</h3>
      <p class="text-sm text-gray-500 dark:text-gray-400 mb-3">{{ t('config.export_hint') }}</p>
      <Button :label="t('config.export')" icon="pi pi-download" severity="secondary" size="small" :loading="exporting" data-testid="config-export" @click="handleExport" />
    </section>

    <section class="mb-8 p-4 border border-gray-200 dark:border-gray-700 rounded-lg">
      <h3 class="font-semibold mb-1">{{ t('config.import_section') }}</h3>
      <p class="text-sm text-gray-500 dark:text-gray-400 mb-3">{{ t('config.import_hint') }}</p>
      <input ref="importFileInput" type="file" accept="application/json" class="hidden" data-testid="config-import-file" @change="onImportFileChange">
      <Button :label="t('config.import_choose_file')" icon="pi pi-upload" severity="secondary" size="small" :loading="importing" data-testid="config-import-choose" @click="importFileInput?.click()" />
    </section>

    <section class="p-4 border border-gray-200 dark:border-gray-700 rounded-lg">
      <h3 class="font-semibold mb-1">{{ t('config.compare_section') }}</h3>
      <p class="text-sm text-gray-500 dark:text-gray-400 mb-3">{{ t('config.compare_hint') }}</p>
      <div class="flex items-center gap-3 mb-3 flex-wrap">
        <input ref="compareAInput" type="file" accept="application/json" class="hidden" data-testid="config-compare-a-file" @change="onCompareFileChange('a', $event)">
        <Button :label="snapshotA ? t('config.compare_a') + ' ✓' : t('config.compare_a')" severity="secondary" size="small" data-testid="config-compare-a" @click="compareAInput?.click()" />
        <input ref="compareBInput" type="file" accept="application/json" class="hidden" data-testid="config-compare-b-file" @change="onCompareFileChange('b', $event)">
        <Button :label="snapshotB ? t('config.compare_b') + ' ✓' : t('config.compare_b')" severity="secondary" size="small" data-testid="config-compare-b" @click="compareBInput?.click()" />
        <Button :label="t('config.compare')" icon="pi pi-sort-alt" size="small" :disabled="!snapshotA || !snapshotB" :loading="comparing" data-testid="config-compare-run" @click="handleCompare" />
      </div>
      <ConfigDiffSummary v-if="compareDiff" :diff="compareDiff" data-testid="config-compare-diff" />
    </section>

    <Dialog v-model:visible="showImportDialog" modal :header="t('config.import_preview_title')" :style="{ width: '40rem' }">
      <ConfigDiffSummary v-if="importDiff" :diff="importDiff" />
      <template #footer>
        <Button :label="t('dialog.no')" severity="secondary" text @click="showImportDialog = false; pendingSnapshot = null" />
        <Button :label="t('config.import_apply')" severity="danger" :loading="applying" data-testid="config-import-apply" @click="confirmImport" />
      </template>
    </Dialog>
  </div>
</template>
