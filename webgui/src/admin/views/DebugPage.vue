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
  <div class="debug-page">
    <h1 class="page-title">
      {{ t('debug.title') }}
    </h1>
    <div class="card">
      <div class="debug-form">
        <div class="form-row">
          <div class="cidr-field">
            <FormField :label="t('debug.cidr')" input-id="cidr-input" :hint="'debug.cidr_hint'">
              <InputText
                id="cidr-input"
                v-model="cidr"
                fluid
                @keyup.enter="analyze"
              />
            </FormField>
          </div>
          <div class="mode-field">
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
          <div class="analyze-btn">
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
      <p class="no-results">{{ t('debug.no_results') }}</p>
    </div>

    <template v-else>
      <div
        v-if="result.full_services.length === 0 && result.partial_services.length === 0"
        class="card mt-3"
      >
        <p class="no-results">{{ t('debug.empty') }}</p>
      </div>

      <div
        v-if="result.full_services.length > 0"
        class="card result-card mt-3"
      >
        <div class="result-header">
          <h2>{{ t('debug.full_coverage') }}</h2>
        </div>
        <div class="result-rows">
          <div
            v-for="item in result.full_services"
            :key="`full-${item.category}-${item.service}`"
            class="result-row"
          >
            <span class="item-name">{{ item.category }} / {{ item.service }}</span>
            <span class="item-pct full-pct">{{ pct(item.percentage) }}</span>
          </div>
        </div>
      </div>

      <div
        v-if="result.partial_services.length > 0"
        class="card result-card mt-3"
      >
        <div class="result-header">
          <h2>{{ t('debug.partial_coverage') }}</h2>
        </div>
        <div class="result-rows">
          <div
            v-for="item in result.partial_services"
            :key="`partial-${item.category}-${item.service}`"
            class="result-row"
          >
            <span class="item-name">{{ item.category }} / {{ item.service }}</span>
            <span class="item-pct partial-pct">{{ pct(item.percentage) }}</span>
          </div>
        </div>
      </div>

      <div
        v-if="result.combined_services.length > 0"
        class="card result-card mt-3"
      >
        <div class="result-header">
          <h2>{{ t('debug.combined_coverage') }}</h2>
        </div>
        <div class="result-rows">
          <div class="result-row combined-row">
            <span class="item-name">{{ t('debug.combined_coverage') }}</span>
            <span class="item-pct combined-pct">{{ pct(result.combined_percentage) }}</span>
          </div>
        </div>
      </div>

      <div
        v-if="result.users.length > 0"
        class="card result-card mt-3"
      >
        <div class="result-header">
          <h2>{{ t('debug.user_impact') }}</h2>
        </div>
        <div class="user-table-wrapper">
          <table class="user-table">
            <thead>
              <tr>
                <th>{{ t('debug.user_name') }}</th>
                <th>{{ t('debug.before') }}</th>
                <th>{{ t('debug.after') }}</th>
                <th>{{ t('debug.matching_services') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="user in result.users"
                :key="`user-${user.name}`"
              >
                <td>{{ user.name }}</td>
                <td>{{ pct(user.before_percentage) }}</td>
                <td>{{ pct(user.after_percentage) }}</td>
                <td class="matches-cell">{{ user.matches?.join(', ') }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.debug-page { max-width: 900px; }
.page-title { margin-bottom: 1.5rem; }

.debug-form {
  padding: 0.5rem 0;
}

.form-row {
  display: flex;
  align-items: flex-end;
  gap: 1rem;
}

.cidr-field { flex: 1; }
.mode-field { width: 220px; }
.analyze-btn { padding-bottom: 1px; }

.result-card { padding: 1.25rem 1.5rem; }

.result-header {
  margin-bottom: 0.75rem;
  padding-bottom: 0.5rem;
  border-bottom: 1px solid var(--p-surface-border);
}

.result-header h2 {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
}

.result-rows {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.result-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0.35rem 0;
}

.item-name {
  color: var(--p-text-color);
}

.item-pct {
  font-weight: 600;
  font-size: 0.9rem;
  min-width: 3.5rem;
  text-align: right;
}

.full-pct { color: var(--p-green-500); }
.partial-pct { color: var(--p-orange-500); }
.combined-pct { color: var(--p-primary-color); }

.combined-row {
  padding: 0.5rem 0;
  border-top: 1px solid var(--p-surface-border);
  margin-top: 0.25rem;
}

.user-table-wrapper {
  overflow-x: auto;
}

.user-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.user-table th {
  text-align: left;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--p-surface-border);
  color: var(--p-text-muted-color);
  font-weight: 600;
  white-space: nowrap;
}

.user-table td {
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--p-surface-border);
}

.matches-cell {
  font-size: 0.8rem;
  color: var(--p-text-muted-color);
}

.no-results {
  color: var(--p-text-muted-color);
  text-align: center;
  padding: 1rem 0;
  margin: 0;
}
</style>
