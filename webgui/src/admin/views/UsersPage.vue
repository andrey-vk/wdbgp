<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'
import apiClient from '@/api/client'
import { useUIStore } from '@/admin/stores/ui'
import type { AxiosResponse } from 'axios'
import type { User, UserSavePayload, Credential, UsersListResponse, UserStatusesResponse } from '@/types/users'
import type { Mode, ModesListResponse } from '@/types/modes'
import InputText from 'primevue/inputtext'
import Dialog from 'primevue/dialog'
import InputNumber from 'primevue/inputnumber'
import Select from 'primevue/select'
import Textarea from 'primevue/textarea'
import ToggleSwitch from 'primevue/toggleswitch'
import Checkbox from 'primevue/checkbox'
import Button from 'primevue/button'
import Tag from 'primevue/tag'
import FormField from '@/components/FormField.vue'

const { t } = useI18n()
const router = useRouter()
const confirmDialog = useConfirm()
const toast = useToast()

const users = ref<User[]>([])
const modes = ref<Mode[]>([])
const credentials = ref<Credential[]>([])
const selected = ref<User | null>(null)
const loading = ref(true)
const saving = ref(false)
const credSaving = ref(false)
const editMode = ref(false)

const form = ref({
  name: '',
  peer_ip: '',
  peer_asn: 0,
  next_hop: '',
  bgp_password: '',
  web_auth: 'network',
  enabled: true,
  active_dial: false,
  catalog_mode_id: 0,
  networks_text: '',
  selection_locked: false,
  catalog_editable: true,
  filter_editable: false,
  filter_override: false,
  filter_mode: 'global',
  filter_allow_text: '',
  filter_deny_text: '',
  password_enabled: false,
  is_new: true,
})

// Credential dialog state
const showAddDialog = ref(false)
const showResetPwDialog = ref(false)
const resetTarget = ref('')
const newCredLogin = ref('')
const newCredPassword = ref('')
const resetPassword = ref('')
const dynamicPeer = ref(false)
const defaultWebAuth = ref('network')
const defaultActiveDial = ref(false)

// Networks normalization: the button/message appear only when the current
// textarea content differs (ignoring order/whitespace) from the minimal,
// masked, fully-merged form the backend would compute — matching what
// apiUsersCreate/Update will actually enforce at save time.
const networksNormalizedSuggestion = ref<string[] | null>(null)
const networksNeedNormalization = ref(false)

const showNetworks = computed(() => {
  const auth = form.value.web_auth || selected.value?.web_auth
  return auth === 'network' || auth === 'both' || auth === 'any'
})
const showCredentials = computed(() => {
  const auth = form.value.web_auth || selected.value?.web_auth
  return auth === 'login' || auth === 'both' || auth === 'any'
})
const networksRequired = computed(() => {
  const auth = form.value.web_auth || selected.value?.web_auth
  return auth === 'network' || auth === 'both'
})

const webAuthOptions = [
  { label: t('users.web_auth_network'), value: 'network' },
  { label: t('users.web_auth_login'), value: 'login' },
  { label: t('users.web_auth_both'), value: 'both' },
  { label: t('users.web_auth_any'), value: 'any' },
]

const filterModeOptions = [
  { label: t('users.filter_mode_global'), value: 'global' },
  { label: t('users.filter_mode_extend'), value: 'extend' },
  { label: t('users.filter_mode_override'), value: 'override' },
]

const filterModeSelect = computed({
  get() {
    if (form.value.filter_override) return 'override'
    if (form.value.filter_editable) return 'extend'
    return 'global'
  },
  set(val: string) {
    form.value.filter_mode = val
    form.value.filter_editable = val !== 'global'
    form.value.filter_override = val === 'override'
  }
})

onMounted(async () => {
  const [usersResp, modesResp, settingsResp] = await Promise.all([
    apiClient.get<UsersListResponse>('/admin/users'),
    apiClient.get<ModesListResponse>('/admin/modes'),
    apiClient.get('/admin/settings'),
  ])
  users.value = usersResp.data.users
  users.value.sort((a, b) => (a.name || '').localeCompare(b.name || ''))
  modes.value = modesResp.data.modes

  // Read configured default_web_auth from settings
  const dw = settingsResp.data.default_web_auth
  defaultWebAuth.value = dw.value ?? dw.default_value

  // New peers should match the current global Active Dial setting, not a
  // hardcoded guess — an admin who's turned it on (or off) globally expects
  // new peers to follow suit.
  const ad = settingsResp.data.active_dial
  defaultActiveDial.value = ad.value ?? ad.default_value

  loading.value = false

  // Restore user selection from UI store (e.g., when returning from selections page)
  const uiStore = useUIStore()
  const savedId = uiStore.lastSelectedUserId
  if (savedId) {
    const user = users.value.find((u: User) => u.id === savedId)
    if (user) selectUser(user)
    uiStore.clearSelectedUser()
  }

  startStatusPolling()
})

onUnmounted(() => {
  stopStatusPolling()
})

function selectUser(user: User) {
  selected.value = user
  form.value = {
    name: user.name,
    peer_ip: user.peer_ip,
    peer_asn: user.peer_asn,
    next_hop: user.next_hop || '',
    bgp_password: '',
    web_auth: user.web_auth,
    enabled: user.enabled,
    active_dial: user.active_dial,
    catalog_mode_id: user.catalog_mode_id,
    networks_text: (user.networks || []).join('\n'),
    selection_locked: user.selection_locked,
    catalog_editable: user.catalog_editable,
    filter_editable: user.filter_editable,
    filter_override: user.filter_override,
    filter_mode: user.filter_mode || 'global',
    filter_allow_text: (user.filter_allow || []).join('\n'),
    filter_deny_text: (user.filter_deny || []).join('\n'),
    password_enabled: user.has_password,
    is_new: false,
  }
  dynamicPeer.value = user.peer_ip === '0.0.0.0' || user.peer_ip === '::'
  editMode.value = false
  networksNeedNormalization.value = false
  networksNormalizedSuggestion.value = null
  loadCredentials(user.id)
}

function startNew() {
  selected.value = null
  credentials.value = []
  form.value = {
    name: '',
    peer_ip: '',
    peer_asn: 0,
    next_hop: '',
    bgp_password: '',
    web_auth: defaultWebAuth.value,
    enabled: true,
    active_dial: defaultActiveDial.value,
    catalog_mode_id: modes.value.length > 0 ? modes.value[0].id : 0,
    networks_text: '',
    selection_locked: false,
    catalog_editable: true,
    filter_editable: false,
    filter_override: false,
    filter_mode: 'global',
    filter_allow_text: '',
    filter_deny_text: '',
    password_enabled: false,
    is_new: true,
  }
  dynamicPeer.value = false
  editMode.value = true
  networksNeedNormalization.value = false
  networksNormalizedSuggestion.value = null
}

function startEdit() {
  editMode.value = true
}

function cancelEdit() {
  if (selected.value) {
    form.value = {
      name: selected.value.name,
      peer_ip: selected.value.peer_ip,
      peer_asn: selected.value.peer_asn,
      next_hop: selected.value.next_hop || '',
      bgp_password: '',
      web_auth: selected.value.web_auth,
      enabled: selected.value.enabled,
      active_dial: selected.value.active_dial,
      catalog_mode_id: selected.value.catalog_mode_id,
      networks_text: (selected.value.networks || []).join('\n'),
      selection_locked: selected.value.selection_locked,
      catalog_editable: selected.value.catalog_editable,
      filter_editable: selected.value.filter_editable,
      filter_override: selected.value.filter_override,
      filter_mode: selected.value.filter_mode || 'global',
      filter_allow_text: (selected.value.filter_allow || []).join('\n'),
      filter_deny_text: (selected.value.filter_deny || []).join('\n'),
      password_enabled: selected.value.has_password,
      is_new: false,
    }
    dynamicPeer.value = selected.value.peer_ip === '0.0.0.0' || selected.value.peer_ip === '::'
  }
  editMode.value = false
  networksNeedNormalization.value = false
  networksNormalizedSuggestion.value = null
}

function onDynamicPeerChange(value: boolean) {
  if (value) {
    form.value.peer_ip = form.value.peer_ip.includes(':') ? '::' : '0.0.0.0'
    form.value.active_dial = false
  } else {
    form.value.peer_ip = ''
  }
}

function peerStateSeverity(state: string): 'success' | 'secondary' | undefined {
  if (state === 'ESTABLISHED') return 'success'
  return 'secondary'
}

function isDynamic(ip: string): boolean {
  return ip === '0.0.0.0' || ip === '::'
}

function webAuthLabel(val: string): string {
  const found = webAuthOptions.find(o => o.value === val)
  return found ? found.label : val
}

function parseNetworksInput(text: string): string[] {
  return text.split('\n').map(s => s.trim()).filter(Boolean)
}

function sameNetworkSet(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  const sortedA = [...a].sort()
  const sortedB = [...b].sort()
  return sortedA.every((v, i) => v === sortedB[i])
}

// Runs on blur: asks the backend for the minimal equivalent form of the
// current input and compares it (order/whitespace ignored) against what's
// actually typed. A mismatch means unmasked host bits, an overlap, or an
// adjacent pair that should be combined — the same rule the save endpoint
// enforces, checked here first so the admin sees it before submitting.
async function checkNetworksNormalization() {
  const current = parseNetworksInput(form.value.networks_text)
  if (current.length === 0) {
    networksNeedNormalization.value = false
    networksNormalizedSuggestion.value = null
    return
  }
  try {
    const resp = await apiClient.post<{ networks: string[] }>('/admin/users/normalize-networks', { networks: current })
    networksNormalizedSuggestion.value = resp.data.networks
    networksNeedNormalization.value = !sameNetworkSet(current, resp.data.networks)
  } catch {
    // Malformed CIDR mid-edit, etc. — don't block on the preview call;
    // the real validation happens at save time via the save endpoint's
    // own error response.
    networksNeedNormalization.value = false
    networksNormalizedSuggestion.value = null
  }
}

function applyNetworksNormalization() {
  if (!networksNormalizedSuggestion.value) return
  form.value.networks_text = networksNormalizedSuggestion.value.join('\n')
  networksNeedNormalization.value = false
}

async function handleSave() {
  if (networksNeedNormalization.value) {
    toast.add({ severity: 'error', summary: t('users.networks_needs_normalization'), life: 4000 })
    return
  }
  if (!form.value.name.trim()) { toast.add({ severity: 'error', summary: t('users.error_name'), life: 3000 }); return }
  if (!form.value.peer_ip.trim()) { toast.add({ severity: 'error', summary: t('users.error_peer_ip'), life: 3000 }); return }
  const networks = form.value.networks_text.split('\n').map(s => s.trim()).filter(Boolean)
  if (networksRequired.value && networks.length === 0) { toast.add({ severity: 'error', summary: t('users.error_networks'), life: 3000 }); return }
  saving.value = true
  try {
    const payload: UserSavePayload = {
      name: form.value.name.trim(),
      peer_ip: form.value.peer_ip.trim(),
      peer_asn: form.value.peer_asn,
      next_hop: form.value.next_hop.trim(),
      bgp_password: form.value.password_enabled ? form.value.bgp_password : '',
      password_enabled: form.value.password_enabled,
      selection_locked: form.value.selection_locked,
      enabled: form.value.enabled,
      filter_override: form.value.filter_override,
      filter_mode: form.value.filter_mode,
      filter_editable: form.value.filter_editable,
      catalog_mode_id: form.value.catalog_mode_id,
      catalog_editable: form.value.catalog_editable,
      active_dial: form.value.active_dial,
      web_auth: form.value.web_auth,
      // Omit entirely when the field is hidden — the backend must not
      // touch a user's existing networks just because this save happened
      // to include an unrelated field change while networks weren't shown.
      networks: showNetworks.value ? networks : undefined,
      filter_allow: form.value.filter_allow_text.split('\n').map(s => s.trim()).filter(s => s !== ''),
      filter_deny: form.value.filter_deny_text.split('\n').map(s => s.trim()).filter(s => s !== ''),
    }
    let resp: AxiosResponse<User>
    if (!selected.value) {
      resp = await apiClient.post<User>('/admin/users', payload)
    } else {
      resp = await apiClient.put<User>('/admin/users/' + selected.value.id, payload)
    }
    await loadList()
    selected.value = resp.data
    form.value = {
      name: resp.data.name,
      peer_ip: resp.data.peer_ip,
      peer_asn: resp.data.peer_asn,
      next_hop: resp.data.next_hop || '',
      bgp_password: '',
      web_auth: resp.data.web_auth,
      enabled: resp.data.enabled,
      active_dial: resp.data.active_dial,
      catalog_mode_id: resp.data.catalog_mode_id,
      networks_text: (resp.data.networks || []).join('\n'),
      selection_locked: resp.data.selection_locked,
      catalog_editable: resp.data.catalog_editable,
      filter_editable: resp.data.filter_editable,
      filter_override: resp.data.filter_override,
      filter_mode: resp.data.filter_mode || 'global',
      filter_allow_text: (resp.data.filter_allow || []).join('\n'),
      filter_deny_text: (resp.data.filter_deny || []).join('\n'),
      password_enabled: resp.data.has_password,
      is_new: false,
    }
    dynamicPeer.value = isDynamic(resp.data.peer_ip)
    editMode.value = false
    networksNeedNormalization.value = false
    networksNormalizedSuggestion.value = null
    toast.add({ severity: 'success', summary: t('users.saved'), life: 3000 })
  } catch (e: unknown) {
    const err = e as { response?: { data?: { error?: string } } }
    const msg = err.response?.data?.error || t('users.save_failed')
    toast.add({ severity: 'error', summary: msg, life: 3000 })
  } finally { saving.value = false }
}

async function handleDelete() {
  if (!selected.value) return
  confirmDialog.require({
    message: t('users.delete_confirm'), header: selected.value.name,
    acceptLabel: t('dialog.yes'), rejectLabel: t('dialog.no'),
    accept: async () => {
      await apiClient.delete('/admin/users/' + selected.value!.id)
      users.value = users.value.filter(u => u.id !== selected.value!.id)
      selected.value = null; editMode.value = false
      toast.add({ severity: 'success', summary: t('users.deleted'), life: 3000 })
    },
  })
}

async function loadList() {
  const resp = await apiClient.get('/admin/users')
  users.value = resp.data.users
  users.value.sort((a, b) => (a.name || '').localeCompare(b.name || ''))
}

let statusTimer: ReturnType<typeof setInterval> | null = null

function startStatusPolling() {
  stopStatusPolling()
  statusTimer = setInterval(fetchStatuses, 5000)
}

function stopStatusPolling() {
  if (statusTimer !== null) {
    clearInterval(statusTimer)
    statusTimer = null
  }
}

async function fetchStatuses() {
  try {
    const resp = await apiClient.get<UserStatusesResponse>('/admin/users/statuses')
    const states = resp.data.peer_states || {}
    for (const u of users.value) {
      const key = u.peer_ip + ':' + u.peer_asn
      u.peer_state = states[key] || ''
    }
  } catch {
    // silent — best-effort polling
  }
}

async function loadCredentials(userId: number) {
  try {
    const resp = await apiClient.get('/admin/users/' + userId + '/credentials')
    credentials.value = resp.data.credentials
  } catch {
    credentials.value = []
  }
}

function openAddDialog() {
  newCredLogin.value = ''
  newCredPassword.value = ''
  showAddDialog.value = true
}

async function handleAddCredential() {
  if (!selected.value || !newCredLogin.value.trim()) return
  credSaving.value = true
  try {
    await apiClient.put('/admin/users/' + selected.value.id + '/credentials', {
      login: newCredLogin.value.trim(),
      password: newCredPassword.value,
    })
    showAddDialog.value = false
    await loadCredentials(selected.value.id)
  } finally { credSaving.value = false }
}

function confirmDeleteCredential(login: string) {
  confirmDialog.require({
    message: 'Delete this credential?',
    header: login,
    acceptLabel: t('users.credential_delete'),
    rejectLabel: t('dialog.no'),
    acceptClass: 'p-button-danger',
    accept: () => deleteCredential(login),
  })
}

async function deleteCredential(login: string) {
  if (!selected.value) return
  credSaving.value = true
  try {
    await apiClient.delete('/admin/users/' + selected.value.id + '/credentials', {
      data: { login },
    })
    await loadCredentials(selected.value.id)
  } finally { credSaving.value = false }
}

function openResetPw(login: string) {
  resetTarget.value = login
  resetPassword.value = ''
  showResetPwDialog.value = true
}

async function handleResetPassword() {
  if (!selected.value || !resetPassword.value) return
  credSaving.value = true
  try {
    await apiClient.put('/admin/users/' + selected.value.id + '/credentials', {
      login: resetTarget.value,
      password: resetPassword.value,
    })
    showResetPwDialog.value = false
    await loadCredentials(selected.value.id)
  } finally { credSaving.value = false }
}

async function toggleEnabled() {
  if (!selected.value) return
  const newEnabled = !selected.value.enabled
  try {
    await apiClient.put('/admin/users/' + selected.value.id, { enabled: newEnabled })
    selected.value.enabled = newEnabled
    await loadList() // refresh the list to show updated state in sidebar
    toast.add({ severity: 'success', summary: newEnabled ? 'User enabled' : 'User disabled', life: 2000 })
  } catch {
    toast.add({ severity: 'error', summary: 'Failed', life: 3000 })
  }
}
</script>

<template>
  <div class="max-w-[1100px]">
    <h1 class="mb-4">
      {{ t('users.title') }}
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
        class="flex-col shrink-0 w-[260px] sticky top-20 self-start overflow-hidden border border-gray-200 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-900 block group-[.is-selected]:hidden md:flex md:group-[.is-selected]:flex"
      >
        <div class="p-3 border-b border-gray-200 dark:border-gray-700">
          <Button
            :label="t('users.add')"
            icon="pi pi-plus"
            severity="primary"
            size="small"
            @click="startNew"
          />
        </div>
        <div>
          <div
            v-for="u in users"
            :key="u.id"
            class="px-3 py-2.5 cursor-pointer border-b border-gray-100 dark:border-gray-800 transition-colors hover:bg-gray-100 dark:hover:bg-gray-800"
            :class="{ 'bg-gray-100 dark:bg-gray-800 border-l-[3px] border-l-primary': selected?.id === u.id }"
            @click="selectUser(u)"
          >
            <div class="font-medium truncate">
              {{ u.name }}
            </div>
            <div class="flex gap-1.5 items-center mt-0.5">
              <Tag
                v-if="u.peer_state"
                :severity="peerStateSeverity(u.peer_state)"
                :value="u.peer_state"
              />
              <Tag
                v-if="!u.enabled"
                severity="warn"
                :value="t('users.disabled_label')"
              />
              <Tag
                v-if="isDynamic(u.peer_ip)"
                severity="info"
                :value="t('users.dynamic_label')"
              />
            </div>
          </div>
        </div>
      </div>
      <div
        class="flex-1 flex flex-col sticky top-20 self-start overflow-hidden border border-gray-200 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-900 hidden group-[.is-selected]:flex md:flex"
      >
        <div
          v-if="!selected && !editMode"
          class="flex-1 flex items-center justify-center min-h-[200px] text-gray-500 dark:text-gray-400"
        >
          <p>{{ t('users.no_selection') }}</p>
        </div>
        <div
          v-else
          class="flex flex-col flex-1 min-h-0"
        >
          <div class="flex flex-wrap items-center gap-2 px-3 py-2.5 border-b border-gray-200 dark:border-gray-700">
            <!-- Back button (mobile only) -->
            <button class="md:hidden" @click="selected = null; editMode = false">←</button>

            <!-- Title: full width on mobile, normal on desktop -->
            <span class="font-semibold truncate basis-full md:basis-auto md:flex-1">{{ selected?.name || (editMode && !selected ? t('users.add') : '') }}</span>

            <!-- Tags + button + switch: second line on mobile -->
            <div class="flex items-center gap-2">
              <Tag v-if="selected?.peer_state" :value="selected.peer_state" :severity="peerStateSeverity(selected.peer_state)" />
              <Tag v-if="!selected?.enabled" severity="warn" :value="t('users.disabled_label')" />
              <Tag v-if="isDynamic(selected?.peer_ip ?? '')" severity="info" :value="t('users.dynamic_label')" />

              <Button
                v-if="selected && !editMode"
                :label="t('users.configure_selections')"
                icon="pi pi-check-square"
                severity="secondary"
                size="small"
                @click="router.push({ name: 'userSelections', params: { id: selected.id } })"
              />

              <!-- Enabled switch (view mode only) -->
              <div v-if="selected && !editMode" class="switch-row">
                <FormField :label="t('users.enabled')" input-id="uenabled-hdr">
                  <ToggleSwitch id="uenabled-hdr" :modelValue="selected.enabled" @change="toggleEnabled" />
                </FormField>
              </div>
            </div>
          </div>
          <!-- View mode -->
          <div
            v-if="!editMode && selected"
            class="flex-1 px-5 py-4 flex flex-col gap-4"
          >
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.name') }}</span><p class="m-0">
                {{ selected.name }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.peer_ip') }}</span><p class="m-0">
                {{ selected.peer_ip }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.peer_asn') }}</span><p class="m-0">
                {{ selected.peer_asn }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.next_hop') }}</span><p class="m-0">
                {{ selected.next_hop || '—' }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.bgp_password') }}</span><p class="m-0">
                {{ selected.has_password ? t('dialog.yes') : t('users.bgp_password_not_set') }}
              </p>
            </div>
            <div class="flex flex-col md:flex-row gap-4 md:gap-8">
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('users.dynamic_peer') }}</span><p class="m-0">
                  {{ isDynamic(selected.peer_ip) ? t('dialog.yes') : t('dialog.no') }}
                </p>
              </div>
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('users.web_auth') }}</span><p class="m-0">
                  {{ webAuthLabel(selected.web_auth) }}
                </p>
              </div>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.active_dial') }}</span><p class="m-0">
                {{ selected.active_dial ? t('dialog.yes') : t('dialog.no') }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.catalog_mode') }}</span><p class="m-0">
                {{ selected.catalog_mode_name || '—' }}
              </p>
            </div>
            <div
              v-if="selected.web_auth !== 'login'"
              class="flex flex-col gap-1"
            >
              <span class="font-medium">{{ t('users.networks') }}</span><p class="m-0">
                {{ selected.networks?.join(', ') || '—' }}
              </p>
            </div>
            <div class="flex flex-col md:flex-row gap-4 md:gap-8">
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('users.selection_locked') }}</span><p class="m-0">
                  {{ selected.selection_locked ? t('dialog.yes') : t('dialog.no') }}
                </p>
              </div>
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('users.catalog_editable') }}</span><p class="m-0">
                  {{ selected.catalog_editable ? t('dialog.yes') : t('dialog.no') }}
                </p>
              </div>
            </div>
            <div class="flex flex-col md:flex-row gap-4 md:gap-8">
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('users.filter_editable') }}</span><p class="m-0">
                  {{ selected.filter_editable ? t('dialog.yes') : t('dialog.no') }}
                </p>
              </div>
              <div class="flex flex-col gap-1">
                <span class="font-medium">{{ t('users.filter_override') }}</span><p class="m-0">
                  {{ selected.filter_override ? t('dialog.yes') : t('dialog.no') }}
                </p>
              </div>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.filter_allow') }}</span><p class="m-0">
                {{ selected.filter_allow?.length ? selected.filter_allow.join(', ') : '—' }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.filter_deny') }}</span><p class="m-0">
                {{ selected.filter_deny?.length ? selected.filter_deny.join(', ') : '—' }}
              </p>
            </div>
            <div
              v-if="selected.web_auth !== 'network'"
              class="flex flex-col gap-1"
            >
              <span class="font-medium">{{ t('users.credentials') }}</span><p class="m-0">
                {{ credentials.length > 0 ? credentials.map(c => c.login).join(', ') : '—' }}
              </p>
            </div>
            <div class="flex flex-col gap-1">
              <span class="font-medium">{{ t('users.peer_state') }}</span><p class="m-0">
                {{ selected.peer_state || '—' }}
              </p>
            </div>
          </div>
          <!-- Edit mode -->
          <div
            v-else
            class="flex-1 px-5 py-4 flex flex-col gap-5"
          >
            <!-- Group: BGP Connection -->
            <div class="flex flex-col gap-3 first:mt-0 first:border-t-0 first:pt-0 border-t border-gray-200 dark:border-gray-700 pt-4 mt-2">
              <h3 class="text-sm font-semibold text-gray-500 dark:text-gray-400 mb-3">{{ t('users.group_bgp') }}</h3>
              <div class="switch-row">
                <FormField
                  :label="t('users.enabled')"
                  :hint="'users.enabled_hint'"
                  input-id="uenabled"
                >
                  <ToggleSwitch
                    id="uenabled"
                    v-model="form.enabled"
                  />
                </FormField>
              </div>
              <FormField
                :label="t('users.name')"
                :hint="'users.name_hint'"
                input-id="uname"
              >
                <InputText
                  id="uname"
                  v-model="form.name"
                  fluid
                />
              </FormField>
              <FormField
                :label="t('users.peer_ip')"
                :hint="'users.peer_ip_hint'"
                input-id="upeerip"
              >
                <InputText
                  id="upeerip"
                  v-model="form.peer_ip"
                  :readonly="dynamicPeer"
                  fluid
                />
              </FormField>
              <FormField
                :label="t('users.dynamic_peer')"
                :hint="'users.dynamic_peer_hint'"
                input-id="udynamic"
              >
                <Checkbox
                  id="udynamic"
                  v-model="dynamicPeer"
                  binary
                  @change="onDynamicPeerChange(dynamicPeer)"
                />
              </FormField>
              <FormField
                :label="t('users.peer_asn')"
                :hint="'users.peer_asn_hint'"
                input-id="uasn"
              >
                <InputNumber
                  id="uasn"
                  v-model="form.peer_asn"
                  :min="0"
                  :max="4294967295"
                  fluid
                />
              </FormField>
              <FormField
                :label="t('users.next_hop')"
                :hint="'users.next_hop_hint'"
                input-id="unhop"
              >
                <InputText
                  id="unhop"
                  v-model="form.next_hop"
                  fluid
                />
              </FormField>
              <div class="switch-row">
                <FormField
                  :label="t('users.bgp_password_enabled')"
                  :hint="'users.bgp_password_enabled_hint'"
                  input-id="upwenabled"
                >
                  <ToggleSwitch
                    id="upwenabled"
                    v-model="form.password_enabled"
                  />
                </FormField>
              </div>
              <FormField
                v-if="form.password_enabled"
                :label="t('users.bgp_password')"
                :hint="form.is_new ? 'users.bgp_password_hint' : 'users.bgp_password_unchanged_hint'"
                input-id="upw"
              >
                <InputText
                  id="upw"
                  type="password"
                  v-model="form.bgp_password"
                  fluid
                />
              </FormField>
              <div class="switch-row">
                <FormField
                  :label="t('users.active_dial')"
                  :hint="'users.active_dial_hint'"
                  input-id="uadial"
                >
                  <ToggleSwitch
                    id="uadial"
                    v-model="form.active_dial"
                    :disabled="dynamicPeer"
                  />
                </FormField>
              </div>
            </div>

            <!-- Group: Authentication -->
            <div class="flex flex-col gap-3 first:mt-0 first:border-t-0 first:pt-0 border-t border-gray-200 dark:border-gray-700 pt-4 mt-2">
              <h3 class="text-sm font-semibold text-gray-500 dark:text-gray-400 mb-3">{{ t('users.group_auth') }}</h3>
              <FormField
                :label="t('users.web_auth')"
                :hint="'users.web_auth_hint'"
                input-id="uwauth"
              >
                <Select
                  id="uwauth"
                  v-model="form.web_auth"
                  :options="webAuthOptions"
                  option-label="label"
                  option-value="value"
                  fluid
                />
              </FormField>
              <FormField
                v-if="showNetworks"
                :label="t('users.networks')"
                :hint="'users.networks_hint'"
                input-id="unets"
              >
                <Textarea
                  id="unets"
                  v-model="form.networks_text"
                  rows="4"
                  fluid
                  @blur="checkNetworksNormalization"
                />
                <div
                  v-if="networksNeedNormalization"
                  class="flex items-center gap-2 mt-1"
                >
                  <span class="text-xs text-orange-600 dark:text-orange-400">{{ t('users.networks_needs_normalization') }}</span>
                  <Button
                    size="small"
                    severity="warn"
                    text
                    icon="pi pi-sparkles"
                    :label="t('users.networks_normalize_button')"
                    data-testid="networks-normalize-button"
                    @click="applyNetworksNormalization"
                  />
                </div>
              </FormField>
              <p
                v-else
                class="m-0 text-sm text-gray-500 dark:text-gray-400"
              >
                {{ t('users.networks_not_needed') }}
              </p>
              <!-- Credentials sub-section -->
              <div
                v-if="selected && showCredentials"
                class="flex flex-col gap-3 pt-2 border-t border-gray-200 dark:border-gray-700"
              >
                <FormField
                  :label="t('users.credentials')"
                  :hint="'users.credentials_hint'"
                  input-id="ucreds"
                >
                  <div class="flex flex-col gap-2">
                    <div
                      v-if="credentials.length === 0"
                      class="text-gray-500 dark:text-gray-400"
                    >
                      —
                    </div>
                    <div
                      v-for="cred in credentials"
                      :key="cred.login"
                      class="flex items-center gap-2"
                    >
                      <span class="flex-1">{{ cred.login }}</span>
                      <Button
                        icon="pi pi-key"
                        severity="secondary"
                        size="small"
                        text
                        :title="t('users.credential_reset_pw')"
                        :loading="credSaving"
                        @click="openResetPw(cred.login)"
                      />
                      <Button
                        icon="pi pi-trash"
                        severity="danger"
                        size="small"
                        text
                        :title="t('users.credential_delete')"
                        :loading="credSaving"
                        @click="confirmDeleteCredential(cred.login)"
                      />
                    </div>
                  </div>
                </FormField>
                <div class="self-start">
                  <Button
                    :label="t('users.credential_add')"
                    icon="pi pi-plus"
                    severity="secondary"
                    size="small"
                    @click="openAddDialog"
                  />
                </div>
              </div>
              <p
                v-else-if="selected"
                class="m-0 text-sm text-gray-500 dark:text-gray-400"
              >
                {{ t('users.credentials_not_needed') }}
              </p>
            </div>

            <!-- Group: Catalog & Access -->
            <div class="flex flex-col gap-3 first:mt-0 first:border-t-0 first:pt-0 border-t border-gray-200 dark:border-gray-700 pt-4 mt-2">
              <h3 class="text-sm font-semibold text-gray-500 dark:text-gray-400 mb-3">{{ t('users.group_catalog') }}</h3>
              <FormField
                :label="t('users.catalog_mode')"
                :hint="'users.catalog_mode_hint'"
                input-id="ucmode"
              >
                <Select
                  id="ucmode"
                  v-model="form.catalog_mode_id"
                  :options="modes"
                  option-label="name"
                  option-value="id"
                  fluid
                />
              </FormField>
              <div class="switch-row">
                <FormField
                  :label="t('users.selection_locked')"
                  :hint="'users.selection_locked_hint'"
                  input-id="uslock"
                >
                  <ToggleSwitch
                    id="uslock"
                    v-model="form.selection_locked"
                  />
                </FormField>
              </div>
              <div
                v-if="!form.selection_locked"
                class="switch-row"
              >
                <FormField
                  :label="t('users.catalog_editable')"
                  :hint="'users.catalog_editable_hint'"
                  input-id="ucated"
                >
                  <ToggleSwitch
                    id="ucated"
                    v-model="form.catalog_editable"
                  />
                </FormField>
              </div>
              <p v-if="form.selection_locked" class="m-0 text-sm text-gray-500 dark:text-gray-400">
                {{ t('users.catalog_editable_hidden') }}
              </p>
            </div>

            <!-- Group: Route Filters -->
            <div class="flex flex-col gap-3 first:mt-0 first:border-t-0 first:pt-0 border-t border-gray-200 dark:border-gray-700 pt-4 mt-2">
              <h3 class="text-sm font-semibold text-gray-500 dark:text-gray-400 mb-3">{{ t('users.group_filters') }}</h3>
              <FormField
                :label="t('users.filter_mode')"
                :hint="'users.filter_mode_hint'"
                input-id="ufmode"
              >
                <Select
                  id="ufmode"
                  v-model="filterModeSelect"
                  :options="filterModeOptions"
                  option-label="label"
                  option-value="value"
                  fluid
                />
              </FormField>
              <template v-if="form.filter_editable">
                <FormField :label="t('users.filter_allow')" :hint="'users.filter_allow_hint'" input-id="ufallow">
                  <Textarea id="ufallow" v-model="form.filter_allow_text" rows="3" fluid />
                </FormField>
                <FormField :label="t('users.filter_deny')" :hint="'users.filter_deny_hint'" input-id="ufdeny">
                  <Textarea id="ufdeny" v-model="form.filter_deny_text" rows="3" fluid />
                </FormField>
              </template>
              <p v-else class="m-0 text-sm text-gray-500 dark:text-gray-400">
                {{ t('users.filter_fields_hidden') }}
              </p>
            </div>
          </div>
          <div class="flex gap-2 px-5 py-3 shrink-0 bg-inherit border-t border-gray-200 dark:border-gray-700">
            <template v-if="!editMode">
              <Button
                :label="t('users.edit')"
                icon="pi pi-pencil"
                severity="primary"
                @click="startEdit"
              />
              <Button
                :label="t('users.delete')"
                icon="pi pi-trash"
                severity="danger"
                text
                @click="handleDelete"
              />
            </template>
            <template v-else>
              <Button
                :label="t('users.save')"
                icon="pi pi-check"
                severity="primary"
                :loading="saving"
                :disabled="networksNeedNormalization"
                @click="handleSave"
              />
              <Button
                :label="t('users.cancel')"
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

  <!-- Add Credential Dialog -->
  <Dialog
    v-model:visible="showAddDialog"
    modal
    :header="t('users.credential_add')"
    class="w-96"
  >
    <div class="flex flex-col gap-4">
      <FormField
        :label="t('users.credential_login')"
        :hint="'users.credential_login_hint'"
        input-id="addCredLogin"
      >
        <InputText
          id="addCredLogin"
          v-model="newCredLogin"
          autocomplete="off"
          fluid
        />
      </FormField>
      <FormField
        :label="t('users.credential_password')"
        :hint="'users.credential_password_hint'"
        input-id="addCredPassword"
      >
        <InputText
          id="addCredPassword"
          v-model="newCredPassword"
          type="password"
          autocomplete="off"
          fluid
        />
      </FormField>
    </div>
    <template #footer>
      <Button label="Cancel" severity="secondary" text @click="showAddDialog = false" />
      <Button :label="t('users.credential_create')" severity="primary" :loading="credSaving" @click="handleAddCredential" />
    </template>
  </Dialog>

  <!-- Reset Password Dialog -->
  <Dialog
    v-model:visible="showResetPwDialog"
    modal
    :header="t('users.credential_reset_pw')"
    class="w-96"
  >
    <p class="m-0 mb-4 text-surface-500 dark:text-surface-400">{{ resetTarget }}</p>
    <FormField
      :label="t('users.credential_password')"
      :hint="'users.credential_password_hint'"
      input-id="resetPwField"
    >
      <InputText
        id="resetPwField"
        v-model="resetPassword"
        type="password"
        autocomplete="off"
        fluid
      />
    </FormField>
    <template #footer>
      <Button label="Cancel" severity="secondary" text @click="showResetPwDialog = false" />
      <Button label="OK" severity="primary" :loading="credSaving" @click="handleResetPassword" />
    </template>
  </Dialog>
</template>

<style scoped>
.switch-row :deep(.form-field){flex-direction:row;align-items:center;gap:.5rem}
.switch-row :deep(.label-row){margin-bottom:0}
</style>
