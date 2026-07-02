<script setup lang="ts">
import { ref, onMounted, watch, onBeforeUnmount } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import apiClient from '@/api/client'
import { settingsSchema } from '@/types/settings'
import { sections } from '@/admin/settingsMeta'
import type { SettingMeta } from '@/admin/settingsMeta'
import SettingField from '@/components/SettingField.vue'

const metaMap: Record<string, SettingMeta> = {}
for (const s of sections) {
    for (const [k, m] of Object.entries(s.fields)) {
        metaMap[k] = m
    }
}
import Button from 'primevue/button'
import Message from 'primevue/message'

const { t } = useI18n()
const toast = useToast()

const loading = ref(true)
const saving = ref(false)
const saved = ref(false)
const dirty = ref(false)
const purging = ref(false)

// Current values: null = use default, non-null = override
const values = ref<Record<string, boolean | number | string | null>>({})
// Effective defaults from backend
const effectiveDefaults = ref<Record<string, boolean | number | string>>({})
// Env override flags from backend
const envOverrides = ref<Record<string, boolean>>({})

onMounted(async () => {
  try {
    const resp = await apiClient.get('/admin/settings')
    const parsed = settingsSchema.partial().parse(resp.data)

    const v: Record<string, boolean | number | string | null> = {}
    const d: Record<string, boolean | number | string> = {}
    const e: Record<string, boolean> = {}

    for (const [key, setting] of Object.entries(parsed)) {
      v[key] = setting.value
      d[key] = setting.default_value
      e[key] = setting.env_override
    }

    values.value = v
    effectiveDefaults.value = d
    envOverrides.value = e
  } finally {
    loading.value = false
  }
})

let initialLoad = true
watch(values, () => {
  if (initialLoad) { initialLoad = false; return }
  dirty.value = true
}, { deep: true })

watch(dirty, (val) => {
  if (val) {
    window.addEventListener('beforeunload', handleBeforeUnload)
  } else {
    window.removeEventListener('beforeunload', handleBeforeUnload)
  }
})

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
  saved.value = false
  saving.value = true
  try {
    const body: Record<string, boolean | number | string | null> = {}
    for (const [key, val] of Object.entries(values.value)) {
      // Skip env-overridden and permanently-readonly fields
      if (envOverrides.value[key] || metaMap[key]?.readonly) continue
      // Skip password fields with empty value (no change)
      if (val === '' || val == null) {
        if (metaMap[key]?.type === 'password') continue
      }
      body[key] = val
    }
    await apiClient.put('/admin/settings', body)
    dirty.value = false
    saved.value = true
  } catch {
    toast.add({ severity: 'error', summary: t('settings.save_error'), life: 5000 })
  } finally {
    saving.value = false
  }
}

async function handlePurgeMetrics() {
  purging.value = true
  try {
    await apiClient.post('/admin/settings/purge-metrics')
    toast.add({ severity: 'success', summary: t('settings.purge_metrics_done'), life: 3000 })
  } catch {
    toast.add({ severity: 'error', summary: t('settings.purge_metrics_error'), life: 3000 })
  } finally { purging.value = false }
}


</script>

<template>
  <div class="max-w-[800px]">
    <h1 class="mb-6">
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
      <div class="flex flex-col gap-6">
        <div
          v-for="section in sections"
          :key="section.name"
          class="card p-6"
        >
          <div class="mb-4 pb-2 border-b border-surface">
            <h2 class="m-0 text-lg">{{ t(section.name) }}</h2>
          </div>
          <div class="flex flex-col gap-6">
            <SettingField
              v-for="(meta, key) in section.fields"
              :key="key"
              :field-key="key"
              :meta="meta"
              :value="values[key] ?? null"
              :default-value="effectiveDefaults[key] ?? ''"
              :env-override="envOverrides[key] ?? false"
              :restart="meta.restart"
              @update:value="(v: boolean | number | string | null) => { values[key] = v }"
            />
          </div>
        </div>
      </div>

      <div class="card p-4 mt-4">
        <h2 class="font-semibold mb-2">{{ t('settings.purge_metrics') }}</h2>
        <p class="text-sm text-gray-500 dark:text-gray-400 mb-3">{{ t('settings.purge_metrics_hint') }}</p>
        <Button
          :label="t('settings.purge_metrics_button')"
          icon="pi pi-trash"
          severity="danger"
          :loading="purging"
          @click="handlePurgeMetrics"
        />
      </div>

      <div class="sticky bottom-0 flex justify-end py-3 mt-4">
        <Button
          :label="t('settings.save')"
          icon="pi pi-check"
          severity="primary"
          class="shadow-lg shadow-primary/40"
          :loading="saving"
          @click="handleSave"
        />
      </div>
    </div>
  </div>
</template>

