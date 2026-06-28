<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import apiClient from '@/api/client'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Message from 'primevue/message'
import FormField from '@/components/FormField.vue'

interface Adapter {
  id: number; name: string; source: string
  revision: number; builtin: boolean
  forked_from?: string; forked_version?: number; requires_review?: boolean
}

const { t } = useI18n()
const confirmDialog = useConfirm()
const toast = useToast()
const adapters = ref<Adapter[]>([])
const selected = ref<Adapter | null>(null)
const form = ref({ name: '', source: '' })
const loading = ref(true)
const saving = ref(false)
const editMode = ref(false)

onMounted(async () => {
  const resp = await apiClient.get('/admin/adapters')
  adapters.value = resp.data.adapters
  adapters.value.sort((a, b) => (a.name || '').localeCompare(b.name || ''))
  loading.value = false
})

function selectAdapter(adapter: Adapter) {
  selected.value = adapter
  form.value = { name: adapter.name, source: adapter.source }
  editMode.value = false
}

function startNew() {
  selected.value = null
  form.value = {
    name: '',
    source: `function sync(feed, api) {
  // feed.url  — URL of the feed being processed
  // api.httpGet(url) — fetch data from a URL
  return [];
}`,
  }
  editMode.value = true
}
function startEdit() {
  editMode.value = true
}
function cancelEdit() {
  if (selected.value) form.value = { name: selected.value.name, source: selected.value.source }
  editMode.value = false
}
async function handleSave() {
  if (!form.value.name.trim()) { toast.add({ severity: 'error', summary: t('adapters.error_name'), life: 3000 }); return }
  if (!form.value.source.trim()) { toast.add({ severity: 'error', summary: t('adapters.error_source'), life: 3000 }); return }
  saving.value = true
  try {
    let resp: any
    if (!selected.value) resp = await apiClient.post('/admin/adapters', form.value)
    else resp = await apiClient.put('/admin/adapters/' + selected.value.id, form.value)
    await loadList()
    selected.value = resp.data
    form.value = { name: resp.data.name, source: resp.data.source }
    editMode.value = false
    toast.add({ severity: 'success', summary: t('adapters.saved'), life: 3000 })
  } finally { saving.value = false }
}

async function handleDelete() {
  if (!selected.value) return
  confirmDialog.require({
    message: t('adapters.delete_confirm'), header: selected.value.name,
    acceptLabel: t('dialog.yes'), rejectLabel: t('dialog.no'),
    accept: async () => {
      await apiClient.delete('/admin/adapters/' + selected.value!.id)
      adapters.value = adapters.value.filter(a => a.id !== selected.value!.id)
      selected.value = null; editMode.value = false
      toast.add({ severity: 'success', summary: t('adapters.deleted'), life: 3000 })
    }
  })
}

async function handleFork() {
  if (!selected.value) return
  confirmDialog.require({
    message: t('adapters.fork_warning'), header: selected.value.name,
    acceptLabel: t('dialog.yes'), rejectLabel: t('dialog.no'),
    accept: async () => {
      const resp = await apiClient.post('/admin/adapters/' + selected.value!.id + '/fork')
      await loadList()
      selected.value = resp.data
      form.value = { name: resp.data.name, source: resp.data.source }
      editMode.value = true
      toast.add({ severity: 'success', summary: t('adapters.saved'), life: 3000 })
    },
  })
}

async function handleAcknowledge() {
  if (!selected.value) return
  const resp = await apiClient.post('/admin/adapters/' + selected.value.id + '/acknowledge')
  const idx = adapters.value.findIndex(a => a.id === selected.value!.id)
  if (idx >= 0) adapters.value[idx] = resp.data
  selected.value = resp.data
  toast.add({ severity: 'success', summary: t('adapters.acknowledged'), life: 3000 })
}

async function loadList() { const resp = await apiClient.get('/admin/adapters'); adapters.value = resp.data.adapters; adapters.value.sort((a, b) => (a.name || '').localeCompare(b.name || '')) }
</script>

<template>
  <div class="max-w-[1100px]">
    <h1 class="mb-4">
      {{ t('adapters.title') }}
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
        class="block group-[.is-selected]:hidden md:block flex-col shrink-0 w-[260px] sticky top-20 self-start overflow-hidden border border-gray-200 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-900"
      >
        <div class="p-3 border-b border-gray-200 dark:border-gray-700">
          <Button
            :label="t('adapters.add')"
            icon="pi pi-plus"
            severity="primary"
            size="small"
            @click="startNew"
          />
        </div>
        <div>
          <div
            v-for="a in adapters"
            :key="a.id"
            class="px-3 py-2.5 cursor-pointer border-b border-gray-100 dark:border-gray-800 transition-colors hover:bg-gray-100 dark:hover:bg-gray-800"
            :class="{ 'border-l-[3px] border-l-[var(--p-primary-color)] bg-gray-100 dark:bg-gray-800': selected?.id === a.id }"
            @click="selectAdapter(a)"
          >
            <div class="font-medium truncate">
              {{ a.name }}
            </div>
            <div class="flex gap-1.5 items-center mt-0.5">
              <Tag
                v-if="a.builtin"
                severity="info"
                :value="t('adapters.builtin_label')"
              />
              <Tag
                v-else-if="a.forked_from"
                severity="warn"
                :value="t('adapters.forked_label')"
              />
              <span class="text-xs text-gray-500 dark:text-gray-400">v{{ a.revision }}</span>
            </div>
          </div>
        </div>
      </div>
      <div
        class="hidden group-[.is-selected]:flex md:flex flex-col flex-1 sticky top-20 self-start overflow-hidden border border-gray-200 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-900"
      >
        <div
          v-if="!selected && !editMode"
          class="flex-1 flex items-center justify-center min-h-[200px] text-gray-500 dark:text-gray-400"
        >
          <p>{{ t('adapters.no_selection') }}</p>
        </div>
        <div
          v-else
          class="flex flex-col flex-1 min-h-0"
        >
          <div class="flex flex-wrap items-center gap-2 px-3 py-2.5 border-b border-gray-200 dark:border-gray-700">
            <button class="md:hidden" @click="selected = null; editMode = false">←</button>
            <span class="font-semibold truncate basis-full md:basis-auto md:flex-1">{{ editMode && !selected ? t('adapters.add') : selected?.name }}</span>
            <div class="flex items-center gap-2">
              <Tag v-if="selected?.builtin" severity="info" :value="t('adapters.builtin_label')" />
              <Tag v-else-if="selected?.forked_from" severity="warn" :value="t('adapters.forked_label')" />
            </div>
          </div>
          <div
            v-if="selected?.requires_review"
            class="px-4 py-3"
          >
            <Message
              severity="warn"
              :closable="false"
            >
              {{ t('adapters.review_needed') }}
              <Button
                :label="t('adapters.acknowledge')"
                severity="warn"
                size="small"
                text
                class="ml-2"
                @click="handleAcknowledge"
              />
            </Message>
          </div>
          <div
            v-if="!editMode && selected"
            class="flex-1 px-5 py-4 flex flex-col gap-4"
          >
            <div
              v-if="selected.builtin"
              class="text-sm text-gray-500 dark:text-gray-400 p-2 bg-gray-50 dark:bg-gray-950 rounded"
            >
              {{ t('adapters.builtin_readonly') }}
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('adapters.name') }}</span><p class="m-0">
                {{ selected.name }}
              </p>
            </div>
            <div
              v-if="selected.forked_from"
              class="flex flex-col gap-1"
            >
              <span class="font-medium">{{ t('adapters.forked_from') }}</span><p class="m-0">
                {{ selected.forked_from }} (v{{ selected.forked_version }})
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('adapters.source') }}</span><pre class="m-0 p-3 font-mono whitespace-pre-wrap overflow-y-auto min-h-24 max-h-[40vh] text-sm border border-gray-200 dark:border-gray-700 rounded bg-gray-50 dark:bg-gray-950">{{ selected.source }}</pre>
            </div>
          </div>
          <div
            v-else
            class="flex-1 px-5 py-4 flex flex-col gap-4"
          >
            <FormField
              :label="t('adapters.name')"
              :hint="'adapters.name_hint'"
              input-id="aname"
            >
              <InputText
                id="aname"
                v-model="form.name"
                fluid
              />
            </FormField>
            <FormField
              :label="t('adapters.source')"
              :hint="'adapters.source_hint'"
              input-id="asource"
            >
              <Textarea
                id="asource"
                v-model="form.source"
                rows="16"
                class="font-mono text-sm"
                fluid
              />
            </FormField>
          </div>
          <div class="flex gap-2 px-5 py-3 shrink-0 bg-inherit border-t border-gray-200 dark:border-gray-700">
            <template v-if="!editMode">
              <Button
                v-if="selected?.builtin"
                :label="t('adapters.fork_edit')"
                icon="pi pi-pencil"
                severity="primary"
                @click="handleFork"
              />
              <Button
                v-else
                :label="t('adapters.edit')"
                icon="pi pi-pencil"
                severity="primary"
                @click="startEdit"
              />
              <Button
                v-if="!selected?.builtin"
                :label="t('adapters.delete')"
                icon="pi pi-trash"
                severity="danger"
                text
                @click="handleDelete"
              />
            </template>
            <template v-else>
              <Button
                :label="t('adapters.save')"
                icon="pi pi-check"
                severity="primary"
                :loading="saving"
                @click="handleSave"
              />
              <Button
                :label="t('adapters.cancel')"
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
