<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import apiClient from '@/api/client'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Paginator from 'primevue/paginator'
import FormField from '@/components/FormField.vue'
import ErrorPage from '@/components/ErrorPage.vue'
import { useAsyncPageLoad } from '@/composables/useAsyncPageLoad'
import { useSequencedRequest } from '@/composables/useSequencedRequest'
import type { AuditLogEntry, AuditLogListResponse } from '@/types/audit-log'

const { t } = useI18n()

const actor = ref('')
const action = ref('')
const objectType = ref('')
const objectID = ref('')

const entries = ref<AuditLogEntry[]>([])
const total = ref(0)
const first = ref(0)
const rows = ref(50)
const expanded = ref<Set<number>>(new Set())

const { loading, loadError, run } = useAsyncPageLoad()
const loadRequest = useSequencedRequest()

async function load(): Promise<void> {
  const token = loadRequest.next()
  await run(async () => {
    const params: Record<string, string | number> = { limit: rows.value, offset: first.value }
    if (actor.value.trim()) params.actor = actor.value.trim()
    if (action.value.trim()) params.action = action.value.trim()
    if (objectType.value.trim()) params.object_type = objectType.value.trim()
    if (objectID.value.trim()) params.object_id = objectID.value.trim()
    const resp = await apiClient.get<AuditLogListResponse>('/admin/audit-log', { params })
    // A newer load (a later filter change or page turn) may have already
    // started and could still finish after this one — applying this
    // response then would show entries matching the controls' old state
    // under whatever filters/page are now displayed.
    if (!loadRequest.isCurrent(token)) return
    entries.value = resp.data.entries
    total.value = resp.data.total
  })
}

function applyFilters(): void {
  first.value = 0
  load()
}

function onPage(e: { first: number; rows: number }): void {
  first.value = e.first
  rows.value = e.rows
  load()
}

function toggleExpanded(id: number): void {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString()
}

onMounted(load)
</script>

<template>
  <div class="max-w-[1100px]">
    <h1 class="mb-6">
      {{ t('audit_log.title') }}
    </h1>
    <ErrorPage v-if="loadError" />
    <template v-else>
      <div class="card">
        <div class="py-2">
          <div class="flex items-end gap-4 flex-wrap">
            <div class="w-[200px]">
              <FormField :label="t('audit_log.actor')" input-id="audit-actor-input">
                <InputText id="audit-actor-input" v-model="actor" fluid @keyup.enter="applyFilters" />
              </FormField>
            </div>
            <div class="w-[200px]">
              <FormField :label="t('audit_log.action')" input-id="audit-action-input">
                <InputText id="audit-action-input" v-model="action" fluid @keyup.enter="applyFilters" />
              </FormField>
            </div>
            <div class="w-[160px]">
              <FormField :label="t('audit_log.object_type')" input-id="audit-object-type-input">
                <InputText id="audit-object-type-input" v-model="objectType" fluid @keyup.enter="applyFilters" />
              </FormField>
            </div>
            <div class="w-[140px]">
              <FormField :label="t('audit_log.object_id')" input-id="audit-object-id-input">
                <InputText id="audit-object-id-input" v-model="objectID" fluid @keyup.enter="applyFilters" />
              </FormField>
            </div>
            <div class="pb-px">
              <Button :label="t('audit_log.filter')" icon="pi pi-filter" severity="primary" data-testid="audit-log-filter-button" @click="applyFilters" />
            </div>
          </div>
        </div>
      </div>

      <div v-if="loading" class="flex justify-content-center py-4">
        <i class="pi pi-spin pi-spinner text-2xl" />
      </div>

      <div v-else-if="!entries.length" class="card mt-3">
        <p class="text-muted-color text-center py-4 m-0">{{ t('audit_log.empty') }}</p>
      </div>

      <template v-else>
        <div class="card mt-3 px-6 py-5">
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm">
              <thead>
                <tr>
                  <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('audit_log.time') }}</th>
                  <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('audit_log.actor') }}</th>
                  <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('audit_log.action') }}</th>
                  <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap">{{ t('audit_log.object') }}</th>
                  <th class="text-left px-3 py-2 border-b border-surface text-muted-color font-semibold whitespace-nowrap" />
                </tr>
              </thead>
              <tbody>
                <template v-for="entry in entries" :key="entry.id">
                  <tr data-testid="audit-log-row">
                    <td class="px-3 py-2 border-b border-surface whitespace-nowrap">{{ formatTime(entry.recorded_at) }}</td>
                    <td class="px-3 py-2 border-b border-surface">{{ entry.actor }}</td>
                    <td class="px-3 py-2 border-b border-surface">{{ entry.action }}</td>
                    <td class="px-3 py-2 border-b border-surface">{{ entry.object_type }}/{{ entry.object_id }}</td>
                    <td class="px-3 py-2 border-b border-surface text-right">
                      <button
                        data-testid="audit-log-toggle"
                        class="text-xs text-primary underline"
                        @click="toggleExpanded(entry.id)"
                      >
                        {{ expanded.has(entry.id) ? t('audit_log.hide_details') : t('audit_log.show_details') }}
                      </button>
                    </td>
                  </tr>
                  <tr v-if="expanded.has(entry.id)" data-testid="audit-log-details">
                    <td colspan="5" class="px-3 py-2 border-b border-surface bg-surface-50 dark:bg-surface-900">
                      <div class="text-xs text-muted-color mb-1">{{ entry.user_agent }}</div>
                      <div class="grid grid-cols-2 gap-3">
                        <div>
                          <div class="font-semibold text-xs mb-1">{{ t('audit_log.before') }}</div>
                          <pre class="text-xs whitespace-pre-wrap break-all">{{ entry.before || '—' }}</pre>
                        </div>
                        <div>
                          <div class="font-semibold text-xs mb-1">{{ t('audit_log.after') }}</div>
                          <pre class="text-xs whitespace-pre-wrap break-all">{{ entry.after || '—' }}</pre>
                        </div>
                      </div>
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>
          <Paginator
            :rows="rows"
            :first="first"
            :total-records="total"
            :rows-per-page-options="[25, 50, 100, 200]"
            class="mt-3"
            @page="onPage"
          />
        </div>
      </template>
    </template>
  </div>
</template>
