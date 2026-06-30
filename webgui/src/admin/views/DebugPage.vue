<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import apiClient from '@/api/client'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Button from 'primevue/button'
import FormField from '@/components/FormField.vue'

const { t } = useI18n()

interface CoverageItem {
  category?: string
  service?: string
  percentage?: number
  before_percentage?: number
  after_percentage?: number
  matches?: string[]
  name?: string
}

interface DebugResult {
  query: string
  full_services: CoverageItem[]
  partial_services: CoverageItem[]
  combined_services: CoverageItem[]
  combined_percentage: number
  users: CoverageItem[]
}

interface Mode {
  id: number
  name: string
}

const cidr = ref('')
const modeId = ref(0)
const modes = ref<Mode[]>([])
const loading = ref(false)
const result = ref<DebugResult | null>(null)
const error = ref('')

onMounted(async () => {
  const resp = await apiClient.get('/admin/modes')
  modes.value = resp.data.modes
  if (modes.value.length > 0) modeId.value = modes.value[0].id
})

async function analyze() {
  if (!cidr.value.trim()) return
  loading.value = true
  error.value = ''
  result.value = null
  try {
    const resp = await apiClient.get('/admin/debug', {
      params: { cidr: cidr.value.trim(), mode: modeId.value },
    })
    result.value = resp.data
  } catch (e: unknown) {
    const err = e as { response?: { data?: { error?: string } } }
    error.value = err.response?.data?.error || 'Analysis failed'
  } finally {
    loading.value = false
  }
}

function pct(v: number | undefined | null): string {
  if (v == null) return '-'
  return Math.round(v) + '%'
}
</script>

<template>
  <div class="max-w-[900px]">
    <h1 class="mb-6">
      {{ t('debug.title') }}
    </h1>
    <div class="card">
      <div class="py-2">
        <div class="flex items-end gap-4">
          <div class="flex-1">
            <FormField :label="t('debug.cidr')" input-id="cidr-input" :hint="'debug.cidr_hint'">
              <InputText
                id="cidr-input"
                v-model="cidr"
                fluid
                @keyup.enter="analyze"
              />
            </FormField>
          </div>
          <div class="w-[220px]">
            <FormField :label="t('debug.mode')" input-id="mode-select">
              <Select
                id="mode-select"
                v-model="modeId"
                :options="modes"
                option-label="name"
                option-value="id"
                fluid
              />
            </FormField>
          </div>
          <div class="pb-px">
            <Button
              :label="t('debug.analyze')"
              icon="pi pi-search"
              severity="primary"
              :loading="loading"
              :disabled="!cidr.trim()"
              @click="analyze"
            />
          </div>
        </div>
      </div>
    </div>

    <div
      v-if="loading"
      class="flex justify-content-center py-4"
    >
      <i class="pi pi-spin pi-spinner text-2xl" />
    </div>

    <div
      v-else-if="error"
      class="card mt-3"
    >
      <p class="text-red-500">{{ error }}</p>
    </div>

    <div
      v-else-if="!result"
      class="card mt-3"
    >
      <p class="text-muted-color text-center py-4 m-0">{{ t('debug.no_results') }}</p>
    </div>

    <template v-else>
      <div
        v-if="result.full_services.length === 0 && result.partial_services.length === 0"
        class="card mt-3"
      >
        <p class="text-muted-color text-center py-4 m-0">{{ t('debug.empty') }}</p>
      </div>

      <div
        v-if="result.full_services.length > 0"
        class="card mt-3 px-6 py-5"
      >
        <div class="mb-3 pb-2 border-b border-surface">
          <h2 class="m-0 text-base font-semibold">{{ t('debug.full_coverage') }}</h2>
        </div>
        <div class="flex flex-col gap-1">
          <div
            v-for="item in result.full_services"
            :key="`full-${item.category}-${item.service}`"
            class="flex justify-between items-center py-1.5"
          >
            <span class="text-color">{{ item.category }} / {{ item.service }}</span>
            <span class="font-semibold text-sm min-w-[3.5rem] text-right text-green-500">{{ pct(item.percentage) }}</span>
          </div>
        </div>
      </div>

      <div
        v-if="result.partial_services.length > 0"
        class="card mt-3 px-6 py-5"
      >
        <div class="mb-3 pb-2 border-b border-surface">
          <h2 class="m-0 text-base font-semibold">{{ t('debug.partial_coverage') }}</h2>
        </div>
        <div class="flex flex-col gap-1">
          <div
            v-for="item in result.partial_services"
            :key="`partial-${item.category}-${item.service}`"
            class="flex justify-between items-center py-1.5"
          >
            <span class="text-color">{{ item.category }} / {{ item.service }}</span>
            <span class="font-semibold text-sm min-w-[3.5rem] text-right text-orange-500">{{ pct(item.percentage) }}</span>
          </div>
        </div>
      </div>

      <div
        v-if="result.combined_services.length > 0"
        class="card mt-3 px-6 py-5"
      >
        <div class="mb-3 pb-2 border-b border-surface">
          <h2 class="m-0 text-base font-semibold">{{ t('debug.combined_coverage') }}</h2>
        </div>
        <div class="flex flex-col gap-1">
          <div class="flex justify-between items-center py-2 border-t border-surface mt-1">
            <span class="text-color">{{ t('debug.combined_coverage') }}</span>
            <span class="font-semibold text-sm min-w-[3.5rem] text-right text-primary">{{ pct(result.combined_percentage) }}</span>
          </div>
        </div>
      </div>

      <div
        v-if="result.users.length > 0"
        class="card mt-3 px-6 py-5"
      >
        <div class="mb-3 pb-2 border-b border-surface">
          <h2 class="m-0 text-base font-semibold">{{ t('debug.user_impact') }}</h2>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr>
                <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('debug.user_name') }}</th>
                <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('debug.before') }}</th>
                <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('debug.after') }}</th>
                <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('debug.matching_services') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="user in result.users"
                :key="`user-${user.name}`"
              >
                <td class="px-3 py-2 border-b border-surface">{{ user.name }}</td>
                <td class="px-3 py-2 border-b border-surface">{{ pct(user.before_percentage) }}</td>
                <td class="px-3 py-2 border-b border-surface">{{ pct(user.after_percentage) }}</td>
                <td class="px-3 py-2 border-b border-surface text-xs text-muted-color">{{ user.matches?.join(', ') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </div>
</template>

