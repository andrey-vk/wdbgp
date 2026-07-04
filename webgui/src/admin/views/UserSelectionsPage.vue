<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Select from 'primevue/select'
import apiClient from '@/api/client'
import { useUIStore } from '@/admin/stores/ui'
import type { UserCatalogResponse } from '@/types/user-selections'

const { t } = useI18n()
const toast = useToast()
const route = useRoute()
const router = useRouter()

const userId = computed(() => Number(route.params.id))

// ── Data state ──────────────────────────────────────────────
const loading = ref(true)
const data = ref<UserCatalogResponse | null>(null)

// ── Checkbox state ──────────────────────────────────────────
const checkedCategories = ref<Set<string>>(new Set())
const checkedServices = ref<Set<string>>(new Set())

// ── Count state ─────────────────────────────────────────────
const countData = ref<{ v4: number; v6: number; delta_v4: number; delta_v6: number } | null>(null)
const countLoading = ref(false)
let debounceTimer: ReturnType<typeof setTimeout> | null = null
let countRequestSeq = 0

// ── Save state ──────────────────────────────────────────────
const saving = ref(false)

// ── Catalog mode ────────────────────────────────────────────
const selectedModeId = ref<number>(0)

// ── Computed ────────────────────────────────────────────────
const catalog = computed<Record<string, string[]>>(() => {
  return data.value?.catalog || {}
})

const categoryNames = computed(() => Object.keys(catalog.value))

function getCategoryCounts(category: string): { v4: number; v6: number } {
  if (!data.value?.prefix_counts) return { v4: 0, v6: 0 }
  const catV4 = data.value.prefix_counts.v4?.[category] || {}
  const catV6 = data.value.prefix_counts.v6?.[category] || {}
  return {
    v4: Object.values(catV4).reduce((a: number, b: number) => a + b, 0),
    v6: Object.values(catV6).reduce((a: number, b: number) => a + b, 0),
  }
}

function getServiceCount(service: string, category: string): number {
  if (!data.value?.prefix_counts) return 0
  const v4 = data.value.prefix_counts.v4?.[category]?.[service] || 0
  const v6 = data.value.prefix_counts.v6?.[category]?.[service] || 0
  return v4 + v6
}

function isCategoryFullyChecked(category: string): boolean {
  return checkedCategories.value.has(category)
}

function isCategoryPartiallyChecked(category: string): boolean {
  const services = catalog.value[category] || []
  if (services.length === 0) return false
  const checkedCount = services.filter((s) => isServiceChecked(s, category)).length
  return checkedCount > 0 && checkedCount < services.length
}

function isServiceChecked(service: string, category: string): boolean {
  if (checkedCategories.value.has(category)) return true
  return checkedServices.value.has(`${category}::${service}`)
}

const totalV4 = computed(() => {
  if (countData.value) return countData.value.v4
  if (!data.value?.prefix_counts) return 0
  let sum = 0
  const v4 = data.value.prefix_counts.v4 || {}
  for (const cat of Object.keys(v4)) {
    for (const svc of Object.keys(v4[cat])) {
      sum += v4[cat][svc] || 0
    }
  }
  return sum
})

const totalV6 = computed(() => {
  if (countData.value) return countData.value.v6
  if (!data.value?.prefix_counts) return 0
  let sum = 0
  const v6 = data.value.prefix_counts.v6 || {}
  for (const cat of Object.keys(v6)) {
    for (const svc of Object.keys(v6[cat])) {
      sum += v6[cat][svc] || 0
    }
  }
  return sum
})

const hasDelta = computed(() => {
  if (!countData.value) return false
  return countData.value.delta_v4 !== 0 || countData.value.delta_v6 !== 0
})

function formatDelta(n: number): string {
  if (n > 0) return t('user.delta_gain', { n })
  if (n < 0) return t('user.delta_loss', { n: Math.abs(n) })
  return ''
}

const userName = computed(() => data.value?.user?.name || '')

// ── Data loading ────────────────────────────────────────────
async function loadData(): Promise<void> {
  loading.value = true
  try {
    const params: Record<string, string> = {}
    if (selectedModeId.value > 0) {
      params.mode = String(selectedModeId.value)
    }
    const resp = await apiClient.get('/admin/users/' + userId.value + '/catalog', { params })
    data.value = resp.data
    selectedModeId.value = resp.data.user?.catalog_mode_id

    // Initialize checkboxes from selections
    const catSet = new Set<string>()
    const svcSet = new Set<string>()

    for (const cat of resp.data.selections?.categories || []) {
      catSet.add(cat)
    }
    for (const svc of resp.data.selections?.services || []) {
      svcSet.add(`${svc.category}::${svc.service}`)
    }

    checkedCategories.value = catSet
    checkedServices.value = svcSet

    await fetchCounts()
  } catch (err) {
    if (import.meta.env.DEV) console.error('Failed to load user catalog', err)
    toast.add({ severity: 'error', summary: 'Failed to load catalog', life: 5000 })
  } finally {
    loading.value = false
  }
}

// ── Mode switch ─────────────────────────────────────────────
async function switchMode(modeId: number): Promise<void> {
  if (modeId === selectedModeId.value) return
  selectedModeId.value = modeId
  await loadData()
}

// ── Checkbox handlers ───────────────────────────────────────
function toggleCategory(category: string): void {
  const newCats = new Set(checkedCategories.value)
  const newSvcs = new Set(checkedServices.value)
  const services = catalog.value[category] || []

  if (newCats.has(category)) {
    newCats.delete(category)
    for (const svc of services) {
      newSvcs.delete(`${category}::${svc}`)
    }
  } else {
    newCats.add(category)
    for (const svc of services) {
      newSvcs.delete(`${category}::${svc}`)
    }
  }

  checkedCategories.value = newCats
  checkedServices.value = newSvcs
  debounceCount()
}

function toggleService(service: string, category: string): void {
  const newCats = new Set(checkedCategories.value)
  const newSvcs = new Set(checkedServices.value)
  const key = `${category}::${service}`
  const services = catalog.value[category] || []

  if (isServiceChecked(service, category)) {
    if (newCats.has(category)) {
      newCats.delete(category)
      for (const svc of services) {
        const k = `${category}::${svc}`
        if (svc !== service) {
          newSvcs.add(k)
        }
      }
    } else {
      newSvcs.delete(key)
    }
  } else {
    if (newCats.has(category)) {
      return
    }
    newSvcs.add(key)
    if (services.every((s) => newSvcs.has(`${category}::${s}`))) {
      newCats.add(category)
      for (const svc of services) {
        newSvcs.delete(`${category}::${svc}`)
      }
    }
  }

  checkedCategories.value = newCats
  checkedServices.value = newSvcs
  debounceCount()
}

// ── Count fetching ──────────────────────────────────────────
function debounceCount(): void {
  if (debounceTimer) clearTimeout(debounceTimer)
  countLoading.value = true
  debounceTimer = setTimeout(() => {
    debounceTimer = null
    fetchCounts()
  }, 300)
}

async function fetchCounts(): Promise<void> {
  const seq = ++countRequestSeq
  const selections = {
    ...buildSelectionPayload(),
    mode_id: selectedModeId.value,
  }
  try {
    const resp = await apiClient.post('/admin/users/' + userId.value + '/count-selections', selections)
    if (seq !== countRequestSeq) return // a newer request superseded this one
    countData.value = resp.data
  } catch (err) {
    if (seq !== countRequestSeq) return
    if (import.meta.env.DEV) console.error('Failed to count selections', err)
    countData.value = null
  } finally {
    if (seq === countRequestSeq) countLoading.value = false
  }
}

function buildSelectionPayload() {
  const categories: { category: string; checked: boolean }[] = []
  const services: { category: string; service: string; checked: boolean }[] = []

  for (const cat of Object.keys(catalog.value)) {
    categories.push({ category: cat, checked: checkedCategories.value.has(cat) })
    for (const svc of (catalog.value[cat] || []) as string[]) {
      services.push({
        category: cat,
        service: svc,
        checked: isServiceChecked(svc, cat),
      })
    }
  }

  return { categories, services }
}

// ── Save ────────────────────────────────────────────────────
async function saveSelections(): Promise<void> {
  saving.value = true
  try {
    await apiClient.put('/admin/users/' + userId.value + '/selections', {
      ...buildSelectionPayload(),
      mode_id: selectedModeId.value,
    })
    toast.add({ severity: 'success', summary: t('user.saved'), life: 3000 })
  } catch (err) {
    if (import.meta.env.DEV) console.error('Failed to save selections', err)
    toast.add({ severity: 'error', summary: 'Error', life: 5000 })
  } finally {
    saving.value = false
  }
}

function goBack() {
  useUIStore().setSelectedUser(userId.value)
  router.push({ name: 'users' })
}

// ── Lifecycle ───────────────────────────────────────────────
onMounted(() => {
  loadData()
})
</script>

<template>
  <div class="max-w-[900px]">
    <!-- Sticky top bar -->
    <div class="sticky top-16 z-10 bg-white dark:bg-gray-900 border-b border-gray-200 dark:border-gray-700 px-5 py-3 flex items-center gap-3">
      <Button icon="pi pi-arrow-left" severity="secondary" text rounded aria-label="Back" @click="goBack" />
      <span class="font-semibold truncate flex-1">{{ t('user.selections_title') }} — {{ userName }}</span>
      <div class="flex gap-2">
        <Button :label="t('user.save')" icon="pi pi-check" severity="primary" size="small" :loading="saving" @click="saveSelections" />
        <Button :label="t('feeds.cancel')" icon="pi pi-times" severity="secondary" size="small" text @click="goBack" />
      </div>
    </div>

    <!-- Body -->
    <div class="px-5 py-4">
      <!-- Loading -->
      <div v-if="loading" class="flex justify-content-center py-4">
        <i class="pi pi-spin pi-spinner text-2xl" />
      </div>

      <!-- Error state -->
      <div v-else-if="!data" class="text-center py-12 text-gray-500 dark:text-gray-400">
        <p class="text-lg mb-2">{{ t('user.load_failed') }}</p>
        <Button :label="t('user.retry')" severity="secondary" size="small" @click="loadData" />
      </div>

      <template v-else>
        <!-- Catalog mode switcher -->
        <div v-if="data?.modes?.length" class="flex items-center gap-3 mb-4">
          <span class="text-sm font-medium text-muted-color">{{ t('user.catalog_mode') }}:</span>
          <Select
            :modelValue="selectedModeId"
            :options="data.modes"
            option-label="name"
            option-value="id"
            size="small"
            @change="switchMode(Number(($event as { value: number }).value))"
          />
        </div>

        <!-- Catalog section -->
        <div class="p-6 rounded-border shadow-sm mb-4 bg-white dark:bg-gray-900">
          <div v-if="!Object.keys(catalog).length" class="text-gray-400 dark:text-gray-500 text-center py-8">
            No catalog data available.
          </div>

          <div v-for="category in categoryNames" :key="category" class="mb-3 last:mb-0">
            <!-- Category row -->
            <div
              class="flex items-center gap-2 py-1.5 cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800/50 rounded px-2 -mx-2 group"
            >
              <div class="relative flex items-center">
                <div
                  v-if="isCategoryPartiallyChecked(category)"
                  class="w-5 h-5 rounded border-2 border-blue-500 bg-blue-500 flex items-center justify-center"
                >
                  <svg class="w-3 h-3 text-white" fill="none" stroke="currentColor" stroke-width="3" viewBox="0 0 24 24">
                    <line x1="5" y1="12" x2="19" y2="12" />
                  </svg>
                </div>
                <div
                  v-else
                  class="w-5 h-5 rounded border-2 flex items-center justify-center transition-colors"
                  :class="isCategoryFullyChecked(category) ? 'bg-blue-500 border-blue-500' : 'border-gray-300 dark:border-gray-600 group-hover:border-blue-400'"
                  @click.stop="toggleCategory(category)"
                >
                  <svg
                    v-if="isCategoryFullyChecked(category)"
                    class="w-3 h-3 text-white"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="3"
                    viewBox="0 0 24 24"
                  >
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                </div>
              </div>
              <span
                class="flex-1 text-sm font-medium text-gray-900 dark:text-white select-none"
                @click="toggleCategory(category)"
              >
                {{ category }}
              </span>
              <span class="text-xs text-gray-400 dark:text-gray-500">
                (+{{ getCategoryCounts(category).v4 + getCategoryCounts(category).v6 }})
              </span>
            </div>

            <!-- Service rows -->
            <div
              v-for="service in catalog[category]"
              :key="service"
              class="flex items-center gap-2 py-1 ml-7 cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800/50 rounded px-2 -mx-2 group"
            >
              <div
                class="w-5 h-5 rounded border-2 flex items-center justify-center transition-colors flex-shrink-0"
                :class="isServiceChecked(service, category) ? 'bg-blue-500 border-blue-500' : 'border-gray-300 dark:border-gray-600 group-hover:border-blue-400'"
                @click="toggleService(service, category)"
              >
                <svg
                  v-if="isServiceChecked(service, category)"
                  class="w-3 h-3 text-white"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="3"
                  viewBox="0 0 24 24"
                >
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              </div>
              <span
                class="flex-1 text-sm text-gray-700 dark:text-gray-300 select-none"
                @click="toggleService(service, category)"
              >
                {{ service }}
              </span>
              <span class="text-xs text-gray-400 dark:text-gray-500">
                {{ getServiceCount(service, category).toLocaleString() }} {{ t('user.prefixes') }}
              </span>
            </div>
          </div>
        </div>

        <!-- Summary section -->
        <div class="p-6 rounded-border shadow-sm mt-4 bg-white dark:bg-gray-900">
          <div class="flex items-center justify-between mb-3">
            <div class="flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400">
              <span v-if="countLoading"><i class="pi pi-spin pi-spinner mr-1" /></span>
              <span>{{ t('user.ipv4') }}:</span>
              <span class="font-semibold text-gray-900 dark:text-white">{{ totalV4.toLocaleString() }} {{ t('user.prefixes') }}</span>
              <span class="mx-2">|</span>
              <span>{{ t('user.ipv6') }}:</span>
              <span class="font-semibold text-gray-900 dark:text-white">{{ totalV6.toLocaleString() }} {{ t('user.prefixes') }}</span>
            </div>
            <Button
              :label="t('user.save')"
              icon="pi pi-check"
              severity="primary"
              size="small"
              :loading="saving"
              @click="saveSelections"
            />
          </div>

          <!-- Delta badges -->
          <div v-if="countData && hasDelta" class="flex items-center gap-3 text-sm">
            <span v-if="countData.delta_v4 !== 0" :class="countData.delta_v4 > 0 ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'">
              {{ formatDelta(countData.delta_v4) }} {{ t('user.ipv4') }}
            </span>
            <span v-if="countData.delta_v6 !== 0" :class="countData.delta_v6 > 0 ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'">
              {{ formatDelta(countData.delta_v6) }} {{ t('user.ipv6') }}
            </span>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

