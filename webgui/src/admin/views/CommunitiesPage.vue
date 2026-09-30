<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import axios from 'axios'
import apiClient from '@/api/client'
import InputNumber from 'primevue/inputnumber'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import ErrorPage from '@/components/ErrorPage.vue'
import { useAsyncPageLoad } from '@/composables/useAsyncPageLoad'

interface CommunityItem {
  category: string; service: string; community: number; auto_community: number
}

// One assignment a reset would renumber, as reported by the server's preview.
interface CommunityChange {
  category: string; service: string; old: number; new: number
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const toast = useToast()

// Reactive rather than a plain constant: if the router-view instance is
// ever reused across a direct navigation between two /modes/:id/communities
// routes (e.g. a future breadcrumb or prev/next link), a plain `const`
// captured once at setup would keep every API call pointed at the original
// mode while the URL/header show a different one.
const modeId = computed(() => Number(route.params.id))
const modeName = ref('')
const communities = ref<CommunityItem[]>([])
const originalValues = ref<Map<string, number>>(new Map())
const loading = ref(true)
const saving = ref(false)
const isDirty = ref(false)
const showResetDialog = ref(false)
const resetChanges = ref<CommunityChange[]>([])
// Identifies the exact mode state the current resetChanges preview was
// computed from. The server checks this again right before applying, and
// rejects it (409, with a fresh preview) if the mode changed in the
// meantime — so this must always be the digest from the preview actually
// shown, never reused across a re-open of the dialog.
const resetDigest = ref('')
const resetLoading = ref(false)
const exporting = ref(false)
// loadData() also runs after save/reset, where a failure is reported by
// that action's own toast, not this page-level fallback — so only the
// composable's error-catching (loadError, run) is used here, wrapping just
// the initial onMounted call; loadData() keeps managing `loading` itself
// since it must also toggle it for those other two call sites.
const { loadError, run } = useAsyncPageLoad()

interface CategoryGroup {
  category: string
  groupItem: CommunityItem | undefined
  serviceItems: CommunityItem[]
}

const groupedCommunities = computed<CategoryGroup[]>(() => {
  const map = new Map<string, { group: CommunityItem | undefined; services: CommunityItem[] }>()
  for (const c of communities.value) {
    if (!map.has(c.category)) map.set(c.category, { group: undefined, services: [] })
    const entry = map.get(c.category)!
    if (c.service === '') entry.group = c
    else entry.services.push(c)
  }
  return Array.from(map.entries()).map(([category, v]) => ({
    category,
    groupItem: v.group,
    serviceItems: v.services,
  }))
})

// Computed set of community values that appear more than once (real-time validation)
const duplicateValues = computed<Set<number>>(() => {
  const seen = new Map<number, number>()
  const dups = new Set<number>()
  for (const c of communities.value) {
    if (c.community === 0) continue
    const count = (seen.get(c.community) || 0) + 1
    seen.set(c.community, count)
    if (count === 2) dups.add(c.community)
  }
  return dups
})

function communityKey(item: CommunityItem): string {
  return item.category + '::' + item.service
}

function markDirty() {
  if (isDirty.value) return
  // Check if any community value differs from original
  for (const c of communities.value) {
    const key = communityKey(c)
    const orig = originalValues.value.get(key)
    if (orig === undefined) continue
    if (orig !== c.community) { isDirty.value = true; return }
  }
}

function handleBeforeUnload(e: BeforeUnloadEvent) {
  if (isDirty.value) {
    e.preventDefault()
  }
}

onBeforeRouteLeave((_to, _from, next) => {
  if (isDirty.value) {
    const leave = confirm(t('communities.unsaved_confirm'))
    if (!leave) return next(false)
  }
  next()
})

onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', handleBeforeUnload)
})

onMounted(async () => {
  window.addEventListener('beforeunload', handleBeforeUnload)
  await run(loadData)
})

async function loadData() {
  loading.value = true
  try {
    // Load mode info
    const modeResp = await apiClient.get('/admin/modes/' + modeId.value)
    modeName.value = modeResp.data.name || modeResp.data.mode?.name || ''

    // Load communities
    const commResp = await apiClient.get('/admin/modes/' + modeId.value + '/communities')
    communities.value = commResp.data.communities || []

    // Store original values
    const orig = new Map<string, number>()
    for (const c of communities.value) {
      orig.set(communityKey(c), c.community)
    }
    originalValues.value = orig
    isDirty.value = false
  } finally { loading.value = false }
}

function findDuplicates(): number[] {
  const seen = new Map<number, number>()
  const dups: number[] = []
  for (const c of communities.value) {
    if (c.community === 0) continue
    const count = (seen.get(c.community) || 0) + 1
    seen.set(c.community, count)
    if (count === 2) dups.push(c.community)
  }
  return dups
}

async function handleSave() {
  const dups = findDuplicates()
  if (dups.length > 0) {
    toast.add({ severity: 'error', summary: t('communities.duplicate', { value: dups[0] }), life: 4000 })
    return
  }
  saving.value = true
  try {
    await apiClient.put('/admin/modes/' + modeId.value + '/communities', {
      communities: communities.value.map(c => ({ category: c.category, service: c.service, community: c.community })),
    })
    toast.add({ severity: 'success', summary: t('communities.saved'), life: 3000 })
    // Reload to get fresh original values
    await loadData()
  } catch {
    toast.add({ severity: 'error', summary: 'Failed to save communities.', life: 3000 })
  } finally { saving.value = false }
}

function handleCancel() {
  if (isDirty.value) {
    const leave = confirm(t('communities.unsaved_confirm'))
    if (!leave) return
  }
  router.push({ name: 'modes' })
}

// A reset renumbers values that downstream routers may have hardcoded into
// their filter policies, so it is two-step: ask the server what would change,
// show that, and only then apply. The server enforces the same thing — an
// unconfirmed POST previews instead of writing.
async function handleReset() {
  resetLoading.value = true
  try {
    const resp = await apiClient.post('/admin/modes/' + modeId.value + '/communities/reset')
    if (resp.data.ok) {
      // Nothing needed confirming (no assignments yet); already applied.
      toast.add({
        severity: 'success',
        summary: t('communities.reset_done', { count: resp.data.generated || 0 }),
        life: 3000,
      })
      await loadData()
      return
    }
    resetChanges.value = resp.data.changes || []
    resetDigest.value = resp.data.digest || ''
    if (resetChanges.value.length === 0) {
      toast.add({ severity: 'info', summary: t('communities.reset_no_changes'), life: 3000 })
      return
    }
    showResetDialog.value = true
  } catch {
    toast.add({ severity: 'error', summary: t('communities.reset_failed'), life: 3000 })
  } finally { resetLoading.value = false }
}

async function confirmReset() {
  resetLoading.value = true
  try {
    const resp = await apiClient.post('/admin/modes/' + modeId.value + '/communities/reset', {
      confirm: true,
      digest: resetDigest.value,
    })
    toast.add({
      severity: 'success',
      summary: t('communities.reset_done', { count: resp.data.generated || 0 }),
      life: 3000,
    })
    showResetDialog.value = false
    await loadData()
  } catch (e: unknown) {
    // The mode changed after this preview was shown (a feed sync, or
    // another admin's edit) — the server refused to apply a renumbering
    // the operator never actually reviewed, and sent back what it looks
    // like now instead. Show that rather than a generic failure, so the
    // operator re-reviews instead of just retrying into the same rejection.
    if (axios.isAxiosError(e) && e.response?.status === 409 && e.response.data?.stale) {
      resetChanges.value = e.response.data.changes || []
      resetDigest.value = e.response.data.digest || ''
      if (resetChanges.value.length === 0) {
        showResetDialog.value = false
        toast.add({ severity: 'info', summary: t('communities.reset_no_changes'), life: 3000 })
      } else {
        toast.add({ severity: 'warn', summary: t('communities.reset_stale'), life: 4000 })
      }
      return
    }
    toast.add({ severity: 'error', summary: t('communities.reset_failed'), life: 3000 })
  } finally { resetLoading.value = false }
}

// The export covers every mode at once (community numbers are per-mode, so a
// single-mode document could not express that) and is fetched through the
// admin session here; scripts use the same URL with the status token.
const exportUrl = window.location.origin + '/api/communities'

async function handleExport() {
  exporting.value = true
  try {
    const resp = await apiClient.get('/communities')
    const text = JSON.stringify(resp.data, null, 2)
    const blob = new Blob([text], { type: 'application/json' })
    const link = document.createElement('a')
    link.href = URL.createObjectURL(blob)
    link.download = 'wdbgp-communities.json'
    link.click()
    URL.revokeObjectURL(link.href)
  } catch {
    toast.add({ severity: 'error', summary: t('communities.export_failed'), life: 3000 })
  } finally { exporting.value = false }
}

async function copyExportUrl() {
  try {
    await navigator.clipboard.writeText(exportUrl)
    toast.add({ severity: 'success', summary: t('communities.export_url_copied'), life: 2000 })
  } catch {
    toast.add({ severity: 'error', summary: t('communities.export_url_copy_failed'), life: 3000 })
  }
}
</script>

<template>
  <div class="max-w-[800px] min-h-[70vh]">
    <!-- Sticky top bar -->
    <div class="sticky top-16 z-10 bg-white dark:bg-gray-900 border-b border-gray-200 dark:border-gray-700 px-5 py-3 flex items-center gap-3">
      <Button icon="pi pi-arrow-left" severity="secondary" text rounded :aria-label="t('communities.back')" @click="handleCancel" />
      <span class="font-semibold truncate flex-1">{{ t('communities.title') }} — {{ modeName }}</span>
      <div class="flex gap-2">
        <Button :label="t('communities.export')" icon="pi pi-download" severity="secondary" size="small" :loading="exporting" @click="handleExport" />
        <Button :label="t('communities.reset')" icon="pi pi-undo" severity="secondary" size="small" :loading="resetLoading" @click="handleReset" />
        <Button :label="t('modes.save')" icon="pi pi-check" severity="primary" size="small" :loading="saving" :disabled="duplicateValues.size > 0" @click="handleSave" />
        <Button :label="t('feeds.cancel')" icon="pi pi-times" severity="secondary" size="small" text @click="handleCancel" />
      </div>
    </div>

    <!-- Body -->
    <div class="px-5 py-4">
      <div v-if="loading" class="flex justify-center py-4">
        <i class="pi pi-spin pi-spinner text-2xl" />
      </div>
      <ErrorPage v-else-if="loadError" />
      <div v-else-if="communities.length === 0" class="py-8 text-center text-gray-500 dark:text-gray-400">
        <p>{{ t('communities.none') }}</p>
      </div>
      <div v-else class="border border-gray-200 dark:border-gray-700 rounded-lg overflow-hidden">
        <template v-for="group in groupedCommunities" :key="group.category">
          <!-- Category row -->
          <div class="flex items-center gap-4 px-4 py-2 border-b-2 border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors max-md:flex-col max-md:items-start max-md:gap-1 max-md:px-3">
            <span class="flex-1 truncate font-semibold">{{ group.category }}</span>
            <div class="flex items-center gap-2 shrink-0" :class="{ 'has-duplicate': duplicateValues.has(group.groupItem?.community ?? 0) && group.groupItem?.community !== 0 }">
              <InputNumber v-if="group.groupItem" :input-id="'comm-grp-' + group.category" v-model="group.groupItem.community" :min="0" class="w-28" @update:model-value="markDirty" />
              <span class="text-gray-400 dark:text-gray-500 text-sm whitespace-nowrap min-w-[5rem]">auto {{ group.groupItem?.auto_community }}</span>
            </div>
          </div>
          <!-- Service rows -->
          <div v-for="item in group.serviceItems" :key="communityKey(item)" class="flex items-center gap-4 px-4 py-2 border-b border-gray-100 dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors last:border-b-0 max-md:flex-col max-md:items-start max-md:gap-1 max-md:px-3">
            <span class="flex-1 truncate text-gray-500 dark:text-gray-400 pl-6">{{ item.service }}</span>
            <div class="flex items-center gap-2 shrink-0" :class="{ 'has-duplicate': duplicateValues.has(item.community) && item.community !== 0 }">
              <InputNumber :input-id="'comm-svc-' + item.category + '-' + item.service" v-model="item.community" :min="0" class="w-28" @update:model-value="markDirty" />
              <span class="text-gray-400 dark:text-gray-500 text-sm whitespace-nowrap min-w-[5rem]">auto {{ item.auto_community }}</span>
            </div>
          </div>
        </template>
      </div>

      <!-- Export hint: the same document scripts poll, and how they reach it -->
      <div v-if="!loading && !loadError" class="mt-4 text-sm text-gray-500 dark:text-gray-400">
        <p class="mb-1">{{ t('communities.export_hint') }}</p>
        <div class="flex items-center gap-2 flex-wrap">
          <code class="px-2 py-1 rounded bg-gray-100 dark:bg-gray-800 break-all">GET {{ exportUrl }}</code>
          <Button icon="pi pi-copy" severity="secondary" text size="small" :aria-label="t('communities.export_url_copy')" @click="copyExportUrl" />
        </div>
      </div>
    </div>

    <!-- Reset preview: exactly what the renumbering would change -->
    <Dialog v-model:visible="showResetDialog" modal :header="t('communities.reset_preview_title')" :style="{ width: '34rem' }">
      <p class="mb-3">{{ t('communities.reset_preview_warning') }}</p>
      <p class="mb-3 font-semibold">{{ t('communities.reset_preview_count', { count: resetChanges.length }) }}</p>
      <div class="max-h-[50vh] overflow-y-auto border border-gray-200 dark:border-gray-700 rounded">
        <div
          v-for="change in resetChanges"
          :key="change.category + '::' + change.service"
          class="flex items-center gap-3 px-3 py-1.5 border-b border-gray-100 dark:border-gray-800 last:border-b-0 text-sm"
        >
          <span class="flex-1 truncate">
            {{ change.category }}<span v-if="change.service" class="text-gray-500 dark:text-gray-400"> / {{ change.service }}</span>
          </span>
          <span class="shrink-0 tabular-nums">
            <span class="text-gray-500 dark:text-gray-400 line-through">{{ change.old || '—' }}</span>
            <i class="pi pi-arrow-right mx-2 text-xs" />
            <span class="font-semibold">{{ change.new || '—' }}</span>
          </span>
        </div>
      </div>
      <template #footer>
        <Button :label="t('dialog.no')" severity="secondary" text @click="showResetDialog = false" />
        <Button :label="t('communities.reset_preview_apply')" severity="danger" :loading="resetLoading" @click="confirmReset" />
      </template>
    </Dialog>
  </div>
</template>

<style scoped>
.has-duplicate :deep(input) {
  border-color: #f87171;
  box-shadow: 0 0 0 1px #f87171;
}
</style>
