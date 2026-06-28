<script setup lang="ts">
import { ref, onMounted, watch, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import apiClient from '@/api/client'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import InputNumber from 'primevue/inputnumber'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import FormField from '@/components/FormField.vue'

const { t } = useI18n()
const toast = useToast()

interface SettingField {
  key: string; name: string; type: string
  options?: Record<string, string>
  value: string; env_override: boolean; env_var: string
  restart: boolean; hint?: string
}
interface SettingSection { name: string; fields: SettingField[] }

const sections = ref<SettingSection[]>([])
const values = ref<Record<string, string | boolean | number | null>>({})
const loading = ref(true)
const saving = ref(false)
const saved = ref(false)
const dirty = ref(false)

onMounted(async () => {
  const resp = await apiClient.get('/admin/settings')
  sections.value = resp.data.sections
  const v: Record<string, string | boolean | number | null> = {}
  for (const s of sections.value) {
    for (const f of s.fields) {
      if (f.type === 'bool') v[f.key] = f.value === 'true'
      else if (f.type === 'number') v[f.key] = f.value ? parseInt(f.value, 10) : null
      else v[f.key] = f.value || ''
    }
  }
  values.value = v
  loading.value = false
  window.addEventListener('beforeunload', handleBeforeUnload)
})

let initialLoad = true
watch(values, () => {
  if (initialLoad) { initialLoad = false; return }
  dirty.value = true
}, { deep: true })

onBeforeRouteLeave((_to, _from, next) => {
  if (dirty.value) {
    const leave = confirm(t('settings.unsaved_confirm'))
    if (!leave) return next(false)
  }
  next()
})

function handleBeforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) {
    e.preventDefault()
  }
}

onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', handleBeforeUnload)
})

async function handleSave() {
  saved.value = false; saving.value = true
  const body: Record<string, string> = {}
  for (const s of sections.value) {
    for (const f of s.fields) {
      if (f.env_override) continue
      body[f.key] = f.type === 'bool'
        ? (values.value[f.key] ? 'true' : 'false')
        : String(values.value[f.key] ?? '')
    }
  }
  await apiClient.put('/admin/settings', body)
  dirty.value = false
  saved.value = true; saving.value = false
}

const purging = ref(false)
async function handlePurgeMetrics() {
  purging.value = true
  try {
    await apiClient.post('/admin/settings/purge-metrics')
    toast.add({ severity: 'success', summary: t('settings.purge_metrics_done'), life: 3000 })
  } catch {
    toast.add({ severity: 'error', summary: t('settings.purge_metrics_error'), life: 3000 })
  } finally { purging.value = false }
}

function selectOpts(f: SettingField) {
  if (!f.options) return []
  return Object.entries(f.options).map(([value, labelKey]) => ({ label: t(labelKey), value }))
}
</script>

<template>
  <div class="settings-page">
    <h1 class="page-title">
      {{ t('menu.settings') }}
    </h1>
    <Message
      v-if="saved"
      severity="success"
      :closable="true"
      @close="saved = false"
    >
      {{ t('settings.saved') }}
    </Message>
    <div
      v-if="loading"
      class="flex justify-content-center py-4"
    >
      <i class="pi pi-spin pi-spinner text-2xl" />
    </div>
    <div v-else>
      <div class="sections">
        <div
          v-for="section in sections"
          :key="section.name"
          class="card section-card"
        >
          <div class="section-header">
            <h2>{{ t(section.name) }}</h2>
          </div>
          <div class="section-rows">
            <FormField
              v-for="field in section.fields"
              :key="field.key"
              :label="t(field.name)"
              :hint="field.hint || undefined"
              :input-id="field.key"
            >
              <template #tags>
                <Tag
                  v-if="field.env_override"
                  severity="warn"
                  value="ENV"
                  :title="t('settings.env_override_hint')"
                />
                <Tag
                  v-if="field.restart"
                  severity="info"
                  :value="t('settings.requires_restart')"
                />
              </template>
              <InputText
                v-if="field.type === 'text' && field.key !== 'filter_allow' && field.key !== 'filter_deny'"
                :id="field.key"
                v-model="values[field.key] as string"
                :disabled="field.env_override"
                fluid
              />
              <Textarea
                v-else-if="field.key === 'filter_allow' || field.key === 'filter_deny'"
                :id="field.key"
                v-model="values[field.key] as string"
                :disabled="field.env_override"
                rows="4"
                fluid
              />
              <InputNumber
                v-else-if="field.type === 'number'"
                :id="field.key"
                v-model="values[field.key] as number"
                :disabled="field.env_override"
                fluid
              />
              <Select
                v-else-if="field.type === 'select'"
                :id="field.key"
                v-model="values[field.key] as string"
                :options="selectOpts(field)"
                option-label="label"
                option-value="value"
                :disabled="field.env_override"
                fluid
              />
              <ToggleSwitch
                v-else-if="field.type === 'bool'"
                :id="field.key"
                v-model="values[field.key] as boolean"
                :disabled="field.env_override"
              />
            </FormField>
          </div>
        </div>
      </div>
      <div class="card p-4 mt-4">
        <h2 class="font-semibold mb-2">{{ t('settings.purge_metrics') }}</h2>
        <p class="text-sm text-gray-500 dark:text-gray-400 mb-3">{{ t('settings.purge_metrics_hint') }}</p>
        <Button :label="t('settings.purge_metrics_button')" icon="pi pi-trash" severity="danger" @click="handlePurgeMetrics" :loading="purging" />
      </div>
      <div class="save-bar">
        <Button
          :label="t('settings.save')"
          icon="pi pi-check"
          severity="primary"
          :loading="saving"
          @click="handleSave"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-page { max-width: 800px; }
.page-title { margin-bottom: 1.5rem; }
.sections { display: flex; flex-direction: column; gap: 1.5rem; }
.section-card { padding: 1.5rem; }
.section-header { margin-bottom: 1rem; padding-bottom: .5rem; border-bottom: 1px solid var(--p-surface-border); }
.section-header h2 { margin: 0; font-size: 1.1rem; }
.section-rows { display: flex; flex-direction: column; gap: 0.75rem; }

.save-bar {
  position: sticky;
  bottom: 0;
  background: var(--p-surface-ground);
  display: flex;
  justify-content: flex-end;
  padding: 0.75rem 0;
  margin-top: 1rem;
  border-top: 1px solid var(--p-surface-border);
}
</style>
