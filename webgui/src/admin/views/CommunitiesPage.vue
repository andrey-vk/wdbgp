<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter, onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import apiClient from '@/api/client'
import InputNumber from 'primevue/inputnumber'
import Button from 'primevue/button'
import ErrorPage from '@/components/ErrorPage.vue'
import { useAsyncPageLoad } from '@/composables/useAsyncPageLoad'

interface CommunityItem {
  category: string; service: string; community: number; auto_community: number
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const confirmDialog = useConfirm()
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

function handleReset() {
  confirmDialog.require({
    message: t('communities.reset_confirm'),
    header: modeName.value,
    acceptLabel: t('dialog.yes'),
    rejectLabel: t('dialog.no'),
    accept: async () => {
      const resp = await apiClient.post('/admin/modes/' + modeId.value + '/communities/reset')
      toast.add({
        severity: 'success',
        summary: t('communities.reset_done', { count: resp.data.generated || 0 }),
        life: 3000,
      })
      await loadData()
    },
  })
}
</script>

<template>
  <div class="max-w-[800px] min-h-[70vh]">
    <!-- Sticky top bar -->
    <div class="sticky top-16 z-10 bg-white dark:bg-gray-900 border-b border-gray-200 dark:border-gray-700 px-5 py-3 flex items-center gap-3">
      <Button icon="pi pi-arrow-left" severity="secondary" text rounded :aria-label="t('communities.back')" @click="handleCancel" />
      <span class="font-semibold truncate flex-1">{{ t('communities.title') }} — {{ modeName }}</span>
      <div class="flex gap-2">
        <Button :label="t('communities.reset')" icon="pi pi-undo" severity="secondary" size="small" @click="handleReset" />
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
    </div>
  </div>
</template>

<style scoped>
.has-duplicate :deep(input) {
  border-color: #f87171;
  box-shadow: 0 0 0 1px #f87171;
}
</style>
