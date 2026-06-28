<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import apiClient from '@/api/client'
import InputText from 'primevue/inputtext'
import ToggleSwitch from 'primevue/toggleswitch'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import Checkbox from 'primevue/checkbox'
import FormField from '@/components/FormField.vue'

interface Mode {
  id: number; key: string; name: string; enabled: boolean; feed_count: number
}
interface FeedItem {
  id: number; name: string; url: string; enabled: boolean; adapter_name: string
}

const { t } = useI18n()
const router = useRouter()
const confirmDialog = useConfirm()
const toast = useToast()

const modes = ref<Mode[]>([])
const selected = ref<Mode | null>(null)
const form = ref({ name: '', enabled: true })
const loading = ref(true)
const saving = ref(false)
const editMode = ref(false)

// Feeds data for view and edit mode
const assignedFeeds = ref<FeedItem[]>([])
const allFeeds = ref<FeedItem[]>([])
const assignedFeedIds = ref<number[]>([])
const loadingFeeds = ref(false)
const savingFeeds = ref(false)

onMounted(async () => {
  const resp = await apiClient.get('/admin/modes')
  modes.value = resp.data.modes
  modes.value.sort((a, b) => (a.name || '').localeCompare(b.name || ''))
  loading.value = false
})

function selectMode(mode: Mode) {
  selected.value = mode
  form.value = { name: mode.name, enabled: mode.enabled }
  editMode.value = false
  loadModeFeeds()
}

function startNew() {
  selected.value = null
  form.value = { name: '', enabled: true }
  editMode.value = true
  assignedFeeds.value = []
  allFeeds.value = []
  assignedFeedIds.value = []
  loadAllFeeds()
}

function startEdit() {
  editMode.value = true
  loadAllFeeds()
}

function cancelEdit() {
  if (selected.value) {
    form.value = { name: selected.value.name, enabled: selected.value.enabled }
  }
  editMode.value = false
  loadModeFeeds()
}

function openCommunities() {
  if (selected.value) router.push({ name: 'communities', params: { id: selected.value.id } })
}

async function toggleModeEnabled() {
  if (!selected.value) return
  const newEnabled = !selected.value.enabled
  try {
    await apiClient.put('/admin/modes/' + selected.value.id, { enabled: newEnabled })
    selected.value.enabled = newEnabled
    await loadList()
    toast.add({ severity: 'success', summary: newEnabled ? 'Mode enabled' : 'Mode disabled', life: 2000 })
  } catch {
    toast.add({ severity: 'error', summary: 'Failed', life: 3000 })
  }
}

async function handleSave() {
  if (!form.value.name.trim()) { toast.add({ severity: 'error', summary: t('modes.error_name'), life: 3000 }); return }
  saving.value = true
  try {
    let resp: any
    if (!selected.value) {
      resp = await apiClient.post('/admin/modes', {
        name: form.value.name,
        enabled: form.value.enabled,
      })
    } else {
      resp = await apiClient.put('/admin/modes/' + selected.value.id, {
        name: form.value.name,
        enabled: form.value.enabled,
      })
    }
    const savedMode: Mode = resp.data
    // Save feed assignments
    savingFeeds.value = true
    try {
      await apiClient.put('/admin/modes/' + savedMode.id + '/feeds', {
        feed_ids: assignedFeedIds.value,
      })
      // Fire-and-forget regenerate communities
      apiClient.post('/admin/modes/' + savedMode.id + '/communities/generate').catch(() => {})
    } catch {
      toast.add({ severity: 'error', summary: t('modes.feeds_save_failed'), life: 3000 })
      savingFeeds.value = false
      return
    } finally { savingFeeds.value = false }
    await loadList()
    selected.value = savedMode
    form.value = { name: savedMode.name, enabled: savedMode.enabled }
    editMode.value = false
    toast.add({ severity: 'success', summary: t('modes.saved'), life: 3000 })
  } finally { saving.value = false }
}

async function handleDelete() {
  if (!selected.value) return
  if (selected.value.id <= 3) {
    toast.add({ severity: 'error', summary: t('modes.builtin_delete_blocked'), life: 3000 })
    return
  }
  confirmDialog.require({
    message: t('modes.delete_confirm'), header: selected.value.name,
    acceptLabel: t('dialog.yes'), rejectLabel: t('dialog.no'),
    accept: async () => {
      await apiClient.delete('/admin/modes/' + selected.value!.id)
      modes.value = modes.value.filter(m => m.id !== selected.value!.id)
      selected.value = null; editMode.value = false
      toast.add({ severity: 'success', summary: t('modes.deleted'), life: 3000 })
    },
  })
}

async function loadList() {
  const resp = await apiClient.get('/admin/modes')
  modes.value = resp.data.modes
  modes.value.sort((a, b) => (a.name || '').localeCompare(b.name || ''))
}

async function loadModeFeeds() {
  if (!selected.value) return
  loadingFeeds.value = true
  try {
    const resp = await apiClient.get('/admin/modes/' + selected.value.id + '/feeds')
    assignedFeeds.value = resp.data.feeds || []
    assignedFeedIds.value = assignedFeeds.value.map((f: FeedItem) => f.id)
  } finally { loadingFeeds.value = false }
}

async function loadAllFeeds() {
  loadingFeeds.value = true
  try {
    const resp = await apiClient.get('/admin/feeds')
    allFeeds.value = resp.data.feeds || []
  } finally { loadingFeeds.value = false }
}

function isFeedAssigned(feedId: number): boolean {
  return assignedFeedIds.value.includes(feedId)
}

function toggleFeed(feedId: number) {
  const idx = assignedFeedIds.value.indexOf(feedId)
  if (idx >= 0) assignedFeedIds.value.splice(idx, 1)
  else assignedFeedIds.value.push(feedId)
}
</script>

<template>
  <div class="max-w-[1100px]">
    <h1 class="mb-4">
      {{ t('modes.title') }}
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
            :label="t('modes.add')"
            icon="pi pi-plus"
            severity="primary"
            size="small"
            @click="startNew"
          />
        </div>
        <div>
          <div
            v-for="m in modes"
            :key="m.id"
            class="px-3 py-2.5 cursor-pointer border-b border-gray-100 dark:border-gray-800 transition-colors hover:bg-gray-100 dark:hover:bg-gray-800"
            :class="{ 'border-l-[3px] border-l-[var(--p-primary-color)] bg-gray-100 dark:bg-gray-800': selected?.id === m.id }"
            @click="selectMode(m)"
          >
            <div class="font-medium truncate">
              {{ m.name }}
            </div>
            <div class="flex gap-1.5 items-center mt-0.5">
              <Tag
                v-if="m.id <= 3"
                severity="info"
                :value="t('modes.builtin')"
              />
              <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('modes.feed_count', { count: m.feed_count }) }}</span>
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
          <p>{{ t('modes.no_selection') }}</p>
        </div>
        <div
          v-else
          class="flex flex-col flex-1 min-h-0"
        >
          <div class="flex flex-wrap items-center gap-2 px-3 py-2.5 border-b border-gray-200 dark:border-gray-700">
            <button class="md:hidden" @click="selected = null; editMode = false">←</button>
            <span class="font-semibold truncate basis-full md:basis-auto md:flex-1">{{ editMode && !selected ? t('modes.add') : selected?.name }}</span>
            <div class="flex items-center gap-2">
              <Tag v-if="selected?.id && selected.id <= 3" severity="info" :value="t('modes.builtin')" />
              <Button v-if="selected && !editMode" :label="t('modes.communities_button')" icon="pi pi-hashtag" severity="secondary" size="small" @click="openCommunities" />
              <div v-if="selected && !editMode" class="switch-row">
                <FormField :label="t('modes.enabled')" input-id="menabled-hdr">
                  <ToggleSwitch id="menabled-hdr" :modelValue="selected.enabled" @change="toggleModeEnabled" />
                </FormField>
              </div>
            </div>
          </div>
          <div
            v-if="!editMode && selected"
            class="flex-1 px-5 py-4 flex flex-col gap-4"
          >
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('modes.name') }}</span><p class="m-0">
                {{ selected.name }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('modes.enabled') }}</span><p class="m-0">
                {{ selected.enabled ? t('dialog.yes') : t('dialog.no') }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('modes.view_feeds') }}</span>
              <div
                v-if="loadingFeeds"
                class="flex justify-content-center py-2"
              >
                <i class="pi pi-spin pi-spinner" />
              </div>
              <div v-else-if="assignedFeeds.length === 0">
                <p class="m-0 text-gray-500 dark:text-gray-400 text-sm">{{ t('modes.no_feeds_assigned') }}</p>
              </div>
              <div
                v-else
                class="flex flex-wrap gap-1.5"
              >
                <span
                  v-for="f in assignedFeeds"
                  :key="f.id"
                  class="text-sm px-1.5 py-0.5 bg-gray-50 dark:bg-gray-950 border border-gray-200 dark:border-gray-700 rounded text-gray-700 dark:text-gray-300"
                >{{ f.name }}</span>
              </div>
            </div>
          </div>
          <div
            v-else
            class="flex-1 px-5 py-4 flex flex-col gap-4"
          >
            <FormField
              :label="t('modes.name')"
              :hint="'modes.name_hint'"
              input-id="mname"
            >
              <InputText
                id="mname"
                v-model="form.name"
                fluid
              />
            </FormField>
            <FormField
              :label="t('modes.enabled')"
              :hint="'modes.enabled_hint'"
              input-id="menabled"
            >
              <ToggleSwitch
                id="menabled"
                v-model="form.enabled"
              />
            </FormField>
            <!-- Feed assignments (edit mode only) -->
            <div class="flex flex-col gap-1.5 mt-1">
              <label class="font-medium">{{ t('modes.view_feeds') }}</label>
              <div
                v-if="loadingFeeds"
                class="flex justify-content-center py-2"
              >
                <i class="pi pi-spin pi-spinner" />
              </div>
              <div v-else-if="allFeeds.length === 0">
                <p class="m-0 text-gray-500 dark:text-gray-400 text-sm">{{ t('modes.no_feeds_assigned') }}</p>
              </div>
              <div
                v-else
                class="flex flex-col gap-1.5"
              >
                <div
                  v-for="feed in allFeeds"
                  :key="feed.id"
                  class="flex items-center gap-2 py-0.5"
                >
                  <Checkbox
                    :model-value="isFeedAssigned(feed.id)"
                    :input-id="'feed-' + feed.id"
                    :binary="true"
                    @change="toggleFeed(feed.id)"
                  />
                  <label
                    :for="'feed-' + feed.id"
                    class="font-medium cursor-pointer overflow-hidden text-ellipsis whitespace-nowrap"
                  >{{ feed.name }}</label>
                  <span class="text-xs text-gray-500 dark:text-gray-400 truncate flex-1 min-w-0">{{ feed.url }}</span>
                  <Tag
                    v-if="!feed.enabled"
                    severity="warn"
                    :value="t('feeds.disabled_label')"
                  />
                </div>
              </div>
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
                v-if="selected && selected.id > 3"
                :label="t('modes.delete')"
                icon="pi pi-trash"
                severity="danger"
                text
                @click="handleDelete"
              />
            </template>
            <template v-else>
              <Button
                :label="t('modes.save')"
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
.switch-row :deep(.form-field){flex-direction:row;align-items:center;gap:.5rem}
.switch-row :deep(.label-row){margin-bottom:0}
</style>
