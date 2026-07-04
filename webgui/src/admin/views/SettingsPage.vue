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
// Snapshot of `values` as last loaded from the backend — used by handleSave
// to only forward fields the admin actually changed.
const savedValues = ref<Record<string, boolean | number | string | null>>({})
// Effective defaults from backend
const effectiveDefaults = ref<Record<string, boolean | number | string>>({})
// Env override flags from backend
const envOverrides = ref<Record<string, boolean>>({})

// Fetches the authoritative settings snapshot from the backend. Called on
// mount, and again after every save attempt (success, warning, or error) —
// apiSettingsPut persists each changed key before it can fail on a later
// step (validation of a different key, or BGP reconciliation), so a non-2xx
// response does not mean nothing changed. Re-fetching keeps the form from
// silently drifting out of sync with what's actually persisted.
async function loadSettings() {
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
  savedValues.value = { ...v }
  effectiveDefaults.value = d
  envOverrides.value = e
}

onMounted(async () => {
  try {
    await loadSettings()
  } finally {
    loading.value = false
  }
})

// Set around any programmatic replacement of values.value (initial load,
// and the reload after a save attempt) so that refresh doesn't get
// mistaken for a user edit and re-arm the dirty/unsaved-changes guard.
let suppressDirtyWatch = true
watch(values, () => {
  if (suppressDirtyWatch) { suppressDirtyWatch = false; return }
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
      const meta = metaMap[key]
      // Whitelist: only forward keys we have metadata for and know are
      // writable. A key with no metaMap entry — e.g. the backend started
      // returning a field settingsMeta.ts was never updated for — is never
      // safe to send, since we can't tell if the backend accepts a write.
      if (!meta || meta.readonly || envOverrides.value[key]) continue
      // Skip password fields with empty value (no change)
      if ((val === '' || val == null) && meta.type === 'password') continue
      // Skip fields whose value hasn't actually changed since the last
      // load — the backend fires each setting's OnChange on every
      // Set/Reset, so forwarding an untouched value here can spuriously
      // mark e.g. a BGP restart as pending for a field the admin never
      // edited.
      if (val === savedValues.value[key]) continue
      body[key] = val
    }
    const resp = await apiClient.put('/admin/settings', body)
    dirty.value = false
    if (resp.data?.warning) {
      toast.add({ severity: 'warn', summary: resp.data.warning, life: 8000 })
    } else {
      saved.value = true
    }
  } catch {
    // apiSettingsPut persists each changed key before it can fail on a
    // later step (e.g. a different key failing validation, or BGP
    // reconciliation) — an error here doesn't mean nothing was saved, so
    // the form must not be left showing what the admin typed as if it
    // were rejected outright.
    toast.add({ severity: 'error', summary: t('settings.save_error'), life: 5000 })
  } finally {
    try {
      suppressDirtyWatch = true
      await loadSettings()
    } catch {
      // Best-effort refresh; if it fails the form just keeps showing
      // whatever it had before the save attempt.
    }
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

// Exposed for SettingsPage.spec.ts, which drives saves and inspects local
// state directly rather than through the DOM — without this, a rename here
// would silently break those tests with no static warning.
defineExpose({ values, envOverrides, saving, dirty, saved, handleSave })
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
              :disabled="saving"
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

