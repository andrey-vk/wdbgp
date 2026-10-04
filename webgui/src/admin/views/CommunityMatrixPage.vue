<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import apiClient from '@/api/client'
import InputText from 'primevue/inputtext'
import Checkbox from 'primevue/checkbox'
import ErrorPage from '@/components/ErrorPage.vue'
import { useAsyncPageLoad } from '@/composables/useAsyncPageLoad'
import { useSequencedRequest } from '@/composables/useSequencedRequest'
import type { CommunityMatrixResponse } from '@/types/community-matrix'

const { t } = useI18n()

const matrix = ref<CommunityMatrixResponse>({ modes: [], categories: [] })
const filter = ref('')
const onlyDivergent = ref(false)

const { loading, loadError } = useAsyncPageLoad()
const loadRequest = useSequencedRequest()

async function load(): Promise<void> {
  const token = loadRequest.next()
  loading.value = true
  loadError.value = false
  try {
    const resp = await apiClient.get<CommunityMatrixResponse>('/admin/communities/matrix')
    if (!loadRequest.isCurrent(token)) return
    matrix.value = resp.data
  } catch {
    if (!loadRequest.isCurrent(token)) return
    loadError.value = true
  } finally {
    if (loadRequest.isCurrent(token)) loading.value = false
  }
}

const visibleRows = computed(() => {
  const needle = filter.value.trim().toLowerCase()
  return matrix.value.categories.filter((row) => {
    if (onlyDivergent.value && !row.divergent) return false
    return !needle || row.category.toLowerCase().includes(needle)
  })
})

const divergentCount = computed(() => matrix.value.categories.filter((row) => row.divergent).length)

onMounted(load)
</script>

<template>
  <div class="max-w-[1100px] min-h-[70vh]">
    <div class="sticky top-16 z-10 bg-white dark:bg-gray-900 border-b border-gray-200 dark:border-gray-700 px-5 py-3 flex items-center gap-3 flex-wrap">
      <span class="font-semibold flex-1">{{ t('community_matrix.title') }}</span>
      <span data-testid="matrix-divergent-count" class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('community_matrix.divergent_count', { count: divergentCount }) }}
      </span>
    </div>

    <div class="px-5 py-4">
      <div class="flex items-center gap-4 mb-4 flex-wrap">
        <InputText v-model="filter" data-testid="matrix-filter" :placeholder="t('community_matrix.filter')" class="w-64" />
        <label class="flex items-center gap-2 text-sm">
          <Checkbox v-model="onlyDivergent" binary input-id="matrix-only-divergent" data-testid="matrix-only-divergent" />
          {{ t('community_matrix.only_divergent') }}
        </label>
      </div>

      <div v-if="loading" class="flex justify-center py-4">
        <i class="pi pi-spin pi-spinner text-2xl" />
      </div>
      <ErrorPage v-else-if="loadError" />
      <div v-else-if="matrix.categories.length === 0" class="py-8 text-center text-gray-500 dark:text-gray-400">
        <p>{{ t('community_matrix.none') }}</p>
      </div>
      <div v-else class="overflow-x-auto border border-gray-200 dark:border-gray-700 rounded-lg">
        <table class="w-full text-sm" data-testid="matrix-table">
          <thead class="bg-gray-50 dark:bg-gray-800">
            <tr>
              <th class="text-left px-4 py-2 font-semibold">{{ t('community_matrix.category') }}</th>
              <th v-for="mode in matrix.modes" :key="mode.id" class="text-right px-4 py-2 font-semibold whitespace-nowrap">
                {{ mode.name }}
                <span v-if="!mode.enabled" class="text-xs font-normal text-gray-500">({{ t('community_matrix.disabled') }})</span>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in visibleRows"
              :key="row.category"
              data-testid="matrix-row"
              :data-divergent="row.divergent ? 'true' : 'false'"
              :class="row.divergent ? 'bg-amber-50 dark:bg-amber-900/20' : ''"
              class="border-t border-gray-100 dark:border-gray-800"
            >
              <td class="px-4 py-2 font-medium">
                {{ row.category }}
                <span v-if="row.divergent" class="ml-2 text-xs text-amber-700 dark:text-amber-400">{{ t('community_matrix.divergent') }}</span>
                <span v-if="row.service_divergence > 0" data-testid="matrix-service-divergence" class="ml-2 text-xs text-amber-700 dark:text-amber-400">{{ t('community_matrix.service_differs', { count: row.service_divergence }) }}</span>
              </td>
              <td
                v-for="mode in matrix.modes"
                :key="mode.id"
                class="px-4 py-2 text-right tabular-nums"
                :data-testid="`matrix-cell-${row.category}-${mode.id}`"
              >
                <span v-if="row.values[mode.id] === null || row.values[mode.id] === undefined" class="text-gray-400" :title="t('community_matrix.missing')">—</span>
                <span v-else>{{ row.values[mode.id] }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
