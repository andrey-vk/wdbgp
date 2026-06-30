<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import apiClient from '@/api/client'
import { formatDateTime } from '@/utils/format'
import type { AxiosResponse } from 'axios'
import type { Feed as ApiFeed } from '@/types/feeds'
import type { AdaptersListResponse } from '@/types/adapters'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import InputNumber from 'primevue/inputnumber'
import Textarea from 'primevue/textarea'
import ToggleSwitch from 'primevue/toggleswitch'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import FormField from '@/components/FormField.vue'

interface Feed extends ApiFeed {
  adapter_name?: string
}

interface AdapterOption {
  id: number; name: string
}

const { t } = useI18n()
const confirmDialog = useConfirm()
const toast = useToast()
const feeds = ref<Feed[]>([])
const adapters = ref<AdapterOption[]>([])
const selected = ref<Feed | null>(null)
const form = ref({
  url: '',
  name: '',
  adapter_id: 0,
  allowed_hosts: '',
  restrict_hosts: true,
  enabled: true,
  data: '',
  sync_interval: 0,
})
const loading = ref(true)
const saving = ref(false)
const syncing = ref(false)
const editMode = ref(false)

onMounted(async () => {
  const [feedsResp, adaptersResp] = await Promise.all([
    apiClient.get('/admin/feeds'),
    apiClient.get<AdaptersListResponse>('/admin/adapters'),
  ])
  feeds.value = feedsResp.data.feeds
  feeds.value.sort((a, b) => (a.name || a.url || '').localeCompare(b.name || b.url || ''))
  adapters.value = adaptersResp.data.adapters.map((a) => ({ id: a.id, name: a.name }))
  loading.value = false
})

function selectFeed(feed: Feed) {
  selected.value = feed
  // Resolve adapter_name from already-loaded adapters list
  const adapter = adapters.value.find((a) => a.id === feed.adapter_id)
  selected.value.adapter_name = adapter?.name
  form.value = {
    url: feed.url,
    name: feed.name,
    adapter_id: feed.adapter_id,
    allowed_hosts: feed.allowed_hosts || '',
    restrict_hosts: feed.restrict_hosts,
    enabled: feed.enabled,
    data: feed.data || '',
    sync_interval: feed.sync_interval,
  }
  editMode.value = false
}

function startNew() {
  selected.value = null
  form.value = {
    url: '',
    name: '',
    adapter_id: adapters.value.length > 0 ? adapters.value[0].id : 0,
    allowed_hosts: '',
    restrict_hosts: true,
    enabled: true,
    data: '',
    sync_interval: 0,
  }
  editMode.value = true
}

function startEdit() {
  editMode.value = true
}

function cancelEdit() {
  if (selected.value) {
    form.value = {
      url: selected.value.url,
      name: selected.value.name,
      adapter_id: selected.value.adapter_id,
      allowed_hosts: selected.value.allowed_hosts || '',
      restrict_hosts: selected.value.restrict_hosts,
      enabled: selected.value.enabled,
      data: selected.value.data || '',
      sync_interval: selected.value.sync_interval,
    }
  }
  editMode.value = false
}

async function handleSave() {
  if (!form.value.url.trim()) { toast.add({ severity: 'error', summary: t('feeds.error_url'), life: 3000 }); return }
  if (!form.value.adapter_id) { toast.add({ severity: 'error', summary: t('feeds.error_adapter'), life: 3000 }); return }
  saving.value = true
  try {
    let resp: AxiosResponse<Feed>
    if (!selected.value) resp = await apiClient.post<Feed>('/admin/feeds', form.value)
    else resp = await apiClient.put<Feed>('/admin/feeds/' + selected.value.id, form.value)
    await loadList()
    selected.value = resp.data
    form.value = {
      url: resp.data.url,
      name: resp.data.name,
      adapter_id: resp.data.adapter_id,
      allowed_hosts: resp.data.allowed_hosts || '',
      restrict_hosts: resp.data.restrict_hosts,
      enabled: resp.data.enabled,
      data: resp.data.data || '',
      sync_interval: resp.data.sync_interval,
    }
    editMode.value = false
    toast.add({ severity: 'success', summary: t('feeds.saved'), life: 3000 })
  } finally { saving.value = false }
}

async function handleDelete() {
  if (!selected.value) return
  confirmDialog.require({
    message: t('feeds.delete_confirm'), header: selected.value.name || selected.value.url,
    acceptLabel: t('dialog.yes'), rejectLabel: t('dialog.no'),
    accept: async () => {
      await apiClient.delete('/admin/feeds/' + selected.value!.id)
      feeds.value = feeds.value.filter(f => f.id !== selected.value!.id)
      selected.value = null; editMode.value = false
      toast.add({ severity: 'success', summary: t('feeds.deleted'), life: 3000 })
    },
  })
}

async function handleSync() {
  if (!selected.value) return
  syncing.value = true
  try {
    await apiClient.post('/admin/feeds/' + selected.value.id + '/sync')
    toast.add({ severity: 'success', summary: t('feeds.synced'), life: 3000 })
    await loadList()
    const updated = feeds.value.find(f => f.id === selected.value!.id)
    if (updated) {
      selected.value = updated
      form.value = {
        url: updated.url,
        name: updated.name,
        adapter_id: updated.adapter_id,
        allowed_hosts: updated.allowed_hosts || '',
        restrict_hosts: updated.restrict_hosts,
        enabled: updated.enabled,
        data: updated.data || '',
        sync_interval: updated.sync_interval,
      }
    }
  } catch {
    toast.add({ severity: 'error', summary: t('feeds.sync_error'), life: 3000 })
  } finally { syncing.value = false }
}

async function handleSyncAll() {
  syncing.value = true
  try {
    await apiClient.post('/admin/feeds/sync-all')
    toast.add({ severity: 'success', summary: t('feeds.synced'), life: 3000 })
    await loadList()
  } catch {
    toast.add({ severity: 'error', summary: t('feeds.sync_error'), life: 3000 })
  } finally { syncing.value = false }
}

async function loadList() {
  const resp = await apiClient.get('/admin/feeds')
  feeds.value = resp.data.feeds
  feeds.value.sort((a, b) => (a.name || a.url || '').localeCompare(b.name || b.url || ''))
}
</script>

<template>
  <div class="max-w-[1100px]">
    <h1 class="mb-4">
      {{ t('feeds.title') }}
    </h1>
    <div
      v-if="loading"
      class="flex justify-content-center py-4"
    >
      <i
        class="pi pi-spin pi-spinner text-2xl"
      />
    </div>
    <div
      v-else
      class="group flex flex-col md:flex-row gap-4 items-start"
      :class="{ 'is-selected': selected || editMode }"
    >
      <div
        class="block group-[.is-selected]:hidden md:flex flex-col shrink-0 w-[260px] sticky top-20 self-start overflow-hidden border border-gray-200 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-900"
      >
        <div class="p-3 border-b border-gray-200 dark:border-gray-700">
          <Button
            :label="t('feeds.add')"
            icon="pi pi-plus"
            severity="primary"
            size="small"
            @click="startNew"
          />
        </div>
        <div>
          <div
            v-for="f in feeds"
            :key="f.id"
            class="px-3 py-2.5 cursor-pointer border-b border-gray-100 dark:border-gray-800 transition-colors hover:bg-gray-100 dark:hover:bg-gray-800"
            :class="{ 'border-l-[3px] border-l-primary bg-gray-100 dark:bg-gray-800': selected?.id === f.id }"
            @click="selectFeed(f)"
          >
            <div class="font-medium truncate">
              {{ f.name || f.url }}
            </div>
            <div class="flex gap-1.5 items-center mt-0.5">
              <Tag
                v-if="!f.enabled"
                severity="warn"
                :value="t('feeds.disabled_label')"
              />
              <Tag
                v-if="f.restrict_hosts"
                severity="info"
                :value="t('feeds.restricted_label')"
              />
              <span
                v-if="f.last_error"
                class="w-2 h-2 rounded-full bg-red-500 shrink-0"
              />
            </div>
          </div>
        </div>
        <div class="p-2 px-3 border-t border-gray-200 dark:border-gray-700">
          <Button
            :label="t('feeds.sync_all')"
            icon="pi pi-refresh"
            severity="secondary"
            size="small"
            :loading="syncing"
            fluid
            @click="handleSyncAll"
          />
        </div>
      </div>
      <div
        class="hidden group-[.is-selected]:flex md:flex flex-col flex-1 sticky top-20 self-start overflow-hidden border border-gray-200 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-900"
      >
        <div
          v-if="!selected && !editMode"
          class="flex-1 flex items-center justify-center min-h-[200px] text-gray-500 dark:text-gray-400"
        >
          <p>{{ t('feeds.no_selection') }}</p>
        </div>
        <div
          v-else
          class="flex flex-col flex-1 min-h-0"
        >
          <div class="flex flex-wrap items-center gap-2 px-3 py-2.5 border-b border-gray-200 dark:border-gray-700">
            <button class="md:hidden" @click="selected = null; editMode = false">←</button>
            <span class="font-semibold truncate basis-full md:basis-auto md:flex-1">{{ editMode && !selected ? t('feeds.add') : (selected?.name || selected?.url) }}</span>
            <div class="flex items-center gap-2">
              <Tag v-if="!selected?.enabled" severity="warn" :value="t('feeds.disabled_label')" />
              <Tag v-if="selected?.restrict_hosts" severity="info" :value="t('feeds.restricted_label')" />
            </div>
          </div>
          <div
            v-if="selected?.last_error"
            class="px-4 py-3"
          >
            <Message
              severity="error"
              :closable="false"
            >
              {{ t('feeds.last_error') }}: {{ formatDateTime(selected.last_error) }}
            </Message>
          </div>
          <div
            v-if="!editMode && selected"
            class="flex-1 px-5 py-4 flex flex-col gap-4"
          >
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('feeds.url') }}</span><p class="m-0">
                {{ selected.url }}
              </p>
            </div>
            <div
              v-if="selected.name"
              class="flex flex-col gap-1"
            >
              <span class="font-medium">{{ t('feeds.name') }}</span><p class="m-0">
                {{ selected.name }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('feeds.adapter') }}</span><p class="m-0">
                {{ selected.adapter_name || '—' }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('feeds.allowed_hosts') }}</span><p class="m-0">
                {{ selected.allowed_hosts || '—' }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('feeds.sync_interval') }}</span><p class="m-0">
                {{ selected.sync_interval ? selected.sync_interval + 's' : t('feeds.sync_interval_global') }}
              </p>
            </div>
            <div
              v-if="selected.data"
              class="flex flex-col gap-1"
            >
              <span class="font-medium">{{ t('feeds.data') }}</span>
              <pre class="m-0 p-2 font-mono text-xs whitespace-pre-wrap max-h-40 overflow-y-auto bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-700 rounded">{{ selected.data }}</pre>
            </div>
            <div class="flex gap-8">
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('feeds.restrict_hosts') }}</span><p class="m-0">
                  {{ selected.restrict_hosts ? t('dialog.yes') : t('dialog.no') }}
                </p>
              </div>
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('feeds.enabled') }}</span><p class="m-0">
                  {{ selected.enabled ? t('dialog.yes') : t('dialog.no') }}
                </p>
              </div>
            </div>
            <div
              v-if="selected.last_success"
              class="flex flex-col gap-1"
            >
              <span class="font-medium">{{ t('feeds.last_success') }}</span><p class="m-0">
                {{ formatDateTime(selected.last_success) }}
              </p>
            </div>
          </div>
          <div
            v-else
            class="flex-1 px-5 py-4 flex flex-col gap-4"
          >
            <FormField
              :label="t('feeds.url')"
              :hint="'feeds.url_hint'"
              input-id="furl"
            >
              <InputText
                id="furl"
                v-model="form.url"
                fluid
              />
            </FormField>
            <FormField
              :label="t('feeds.name')"
              :hint="'feeds.name_hint'"
              input-id="fname"
            >
              <InputText
                id="fname"
                v-model="form.name"
                fluid
              />
            </FormField>
            <FormField
              :label="t('feeds.adapter')"
              :hint="'feeds.adapter_hint'"
              input-id="fadapter"
            >
              <Select
                id="fadapter"
                v-model="form.adapter_id"
                :options="adapters"
                option-label="name"
                option-value="id"
                fluid
              />
            </FormField>
            <FormField
              :label="t('feeds.allowed_hosts')"
              :hint="'feeds.allowed_hosts_hint'"
              input-id="fhosts"
            >
              <InputText
                id="fhosts"
                v-model="form.allowed_hosts"
                fluid
              />
            </FormField>
            <FormField
              :label="t('feeds.data')"
              :hint="'feeds.data_hint'"
              input-id="fdata"
            >
              <Textarea
                id="fdata"
                v-model="form.data"
                rows="5"
                class="font-mono text-sm"
                fluid
              />
            </FormField>
            <FormField
              :label="t('feeds.sync_interval')"
              :hint="'feeds.sync_interval_hint'"
              input-id="fsyncint"
            >
              <InputNumber
                id="fsyncint"
                v-model="form.sync_interval"
                :min="0"
                fluid
              />
            </FormField>
            <div class="flex gap-8">
              <FormField
                :label="t('feeds.restrict_hosts')"
                :hint="'feeds.restrict_hosts_hint'"
                input-id="frestrict"
              >
                <ToggleSwitch
                  id="frestrict"
                  v-model="form.restrict_hosts"
                />
              </FormField>
              <FormField
                :label="t('feeds.enabled')"
                :hint="'feeds.enabled_hint'"
                input-id="fenabled"
              >
                <ToggleSwitch
                  id="fenabled"
                  v-model="form.enabled"
                />
              </FormField>
            </div>
          </div>
          <div class="flex gap-2 px-5 py-3 shrink-0 bg-inherit border-t border-gray-200 dark:border-gray-700">
            <template v-if="!editMode">
              <Button
                :label="t('feeds.edit')"
                icon="pi pi-pencil"
                severity="primary"
                @click="startEdit"
              />
              <Button
                :label="t('feeds.sync')"
                icon="pi pi-refresh"
                severity="secondary"
                :loading="syncing"
                @click="handleSync"
              />
              <Button
                :label="t('feeds.delete')"
                icon="pi pi-trash"
                severity="danger"
                text
                @click="handleDelete"
              />
            </template>
            <template v-else>
              <Button
                :label="t('feeds.save')"
                icon="pi pi-check"
                severity="primary"
                :loading="saving"
                @click="handleSave"
              />
              <Button
                :label="t('feeds.cancel')"
                icon="pi pi-times"
                severity="secondary"
                text
                @click="cancelEdit"
              />
            </template>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
</style>
