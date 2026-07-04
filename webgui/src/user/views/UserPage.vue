<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import axios from 'axios'
import Textarea from 'primevue/textarea'
import FormField from '@/components/FormField.vue'
import LanguageSwitcher from '@/components/LanguageSwitcher.vue'
import { useThemeStore } from '@/admin/stores/theme'
import { getCurrentLocale } from '@/plugins/i18n'
import userApi from '@/api/client'
import type { UserDataResponse } from '@/types/user-page'

const { t } = useI18n()
const toast = useToast()
const themeStore = useThemeStore()
const locale = ref(getCurrentLocale())

// ── Auth state ──────────────────────────────────────────────
const authenticated = ref(false)
const authChecking = ref(true)

const loginForm = reactive({ login: '', password: '' })
const loginError = ref('')
const loginLoading = ref(false)

// ── Data state ──────────────────────────────────────────────
const loading = ref(false)
const data = ref<UserDataResponse | null>(null)

// ── Checkbox state ──────────────────────────────────────────
// Fully checked categories (when all services are selected)
const checkedCategories = ref<Set<string>>(new Set())
// Individually checked services: "category::service"
const checkedServices = ref<Set<string>>(new Set())

// ── Count state ─────────────────────────────────────────────
const countData = ref<{ v4: number; v6: number; delta_v4: number; delta_v6: number } | null>(null)
const countLoading = ref(false)
let debounceTimer: ReturnType<typeof setTimeout> | null = null

// ── Save state ──────────────────────────────────────────────
const saving = ref(false)
const savingFilters = ref(false)

// ── Filter state ────────────────────────────────────────────
const filterAllow = ref('')
const filterDeny = ref('')

// ── Catalog mode ────────────────────────────────────────────
const selectedModeId = ref<number>(0)

// ── Computed ────────────────────────────────────────────────
const catalog = computed<Record<string, string[]>>(() => {
  return data.value?.catalog || {}
})

const categoryNames = computed(() => Object.keys(catalog.value))

function getCategoryCounts(category: string): { v4: number; v6: number } {
  if (!data.value?.prefix_counts) return { v4: 0, v6: 0 }
  const catV4 = data.value.prefix_counts.v4?.[category] || {}
  const catV6 = data.value.prefix_counts.v6?.[category] || {}
  return {
    v4: Object.values(catV4).reduce((a: number, b: number) => a + b, 0),
    v6: Object.values(catV6).reduce((a: number, b: number) => a + b, 0),
  }
}

function getServiceCount(service: string, category: string): number {
  if (!data.value?.prefix_counts) return 0
  const v4 = data.value.prefix_counts.v4?.[category]?.[service] || 0
  const v6 = data.value.prefix_counts.v6?.[category]?.[service] || 0
  return v4 + v6
}

function isCategoryFullyChecked(category: string): boolean {
  return checkedCategories.value.has(category)
}

function isCategoryPartiallyChecked(category: string): boolean {
  const services = catalog.value[category] || []
  if (services.length === 0) return false
  const checkedCount = services.filter((s) => isServiceChecked(s, category)).length
  return checkedCount > 0 && checkedCount < services.length
}

function isServiceChecked(service: string, category: string): boolean {
  if (checkedCategories.value.has(category)) return true
  return checkedServices.value.has(`${category}::${service}`)
}

// countData holds the live, selection-aware count from
// /user/count-prefixes. data.value.prefix_counts is the FULL CATALOG's
// per-service counts (used elsewhere for the per-row "(+N)" badges,
// regardless of what's selected) — summing it as a fallback here would
// show "how big the whole catalog is," not "how many prefixes I've
// selected," which is actively misleading when countData is unavailable
// (e.g. the count-prefixes fetch failed). 0 is the honest default.
const totalV4 = computed(() => countData.value?.v4 ?? 0)
const totalV6 = computed(() => countData.value?.v6 ?? 0)

const hasDelta = computed(() => {
  if (!countData.value) return false
  return countData.value.delta_v4 !== 0 || countData.value.delta_v6 !== 0
})

function formatDelta(n: number): string {
  if (n > 0) return t('user.delta_gain', { n })
  if (n < 0) return t('user.delta_loss', { n: Math.abs(n) })
  return ''
}

// ── Auth functions ──────────────────────────────────────────
// Detects a 401 (expired/invalid session) and resets local auth state so
// the login screen shows again. Without this, checkAuth was the only
// function that ever noticed a 401 — every other authenticated action
// (reload, mode switch, count fetch, save) swallowed it generically,
// leaving the UI stuck on a non-functional authenticated view until a
// manual page reload. Returns true if it handled the error, so the caller
// can skip its own generic error toast for this case.
function handleAuthError(err: unknown): boolean {
  if (axios.isAxiosError(err) && err.response?.status === 401) {
    authenticated.value = false
    return true
  }
  return false
}

async function checkAuth(): Promise<void> {
  authChecking.value = true
  try {
    const resp = await userApi.get('/user/me')
    if (resp.data?.user) {
      authenticated.value = true
      await loadUserData(resp.data)
    }
  } catch (err: unknown) {
    handleAuthError(err)
  } finally {
    authChecking.value = false
  }
}

async function handleLogin(): Promise<void> {
  loginError.value = ''
  loginLoading.value = true
  try {
    const resp = await userApi.post('/user/login', {
      login: loginForm.login,
      password: loginForm.password,
    })
    if (resp.data?.user?.id) {
      authenticated.value = true
      await loadUserData(resp.data as UserDataResponse)
    }
  } catch {
    loginError.value = t('user.login_error')
  } finally {
    loginLoading.value = false
  }
}

async function handleLogout(): Promise<void> {
  try {
    await userApi.post('/user/logout')
  } catch {
    // ignore
  }
  authenticated.value = false
  data.value = null
  countData.value = null
  checkedCategories.value = new Set()
  checkedServices.value = new Set()
  loginForm.login = ''
  loginForm.password = ''
}

// ── Data loading ────────────────────────────────────────────
async function loadUserData(userData: UserDataResponse): Promise<void> {
  data.value = userData
  selectedModeId.value = userData.user.catalog_mode_id

  // Initialize checkboxes from selections
  const catSet = new Set<string>()
  const svcSet = new Set<string>()

  for (const cat of userData.selections?.categories || []) {
    catSet.add(cat)
  }
  for (const svc of userData.selections?.services || []) {
    svcSet.add(`${svc.category}::${svc.service}`)
  }

  checkedCategories.value = catSet
  checkedServices.value = svcSet

  // Initialize filter text
  filterAllow.value = (userData.filters?.allow || []).join('\n')
  filterDeny.value = (userData.filters?.deny || []).join('\n')

  // Fetch live counts
  await fetchCounts()
}

async function reloadUserData(): Promise<void> {
  loading.value = true
  try {
    const resp = await userApi.get('/user/me')
    await loadUserData(resp.data)
  } catch (err) {
    handleAuthError(err)
  } finally {
    loading.value = false
  }
}

// ── Mode switch ─────────────────────────────────────────────
async function switchMode(modeId: number): Promise<void> {
  if (modeId === selectedModeId.value) return
  try {
    await userApi.put('/user/mode', { mode_id: modeId })
    selectedModeId.value = modeId
    await reloadUserData()
    toast.add({ severity: 'success', summary: t('user.saved'), life: 3000 })
  } catch (err) {
    if (handleAuthError(err)) return
    toast.add({ severity: 'error', summary: t('user.save_error'), life: 5000 })
    // The backend commits the mode change before it can fail on a later
    // step (e.g. BGP reconciliation), so an error here doesn't mean the
    // switch didn't happen — resync from the server's true state rather
    // than leaving the UI showing pre-switch mode/catalog/selections.
    try {
      await reloadUserData()
    } catch {
      // best-effort; if this also fails, UI keeps showing pre-switch state
    }
  }
}

// ── Checkbox handlers ───────────────────────────────────────
function toggleCategory(category: string): void {
  const newCats = new Set(checkedCategories.value)
  const newSvcs = new Set(checkedServices.value)
  const services = catalog.value[category] || []

  if (newCats.has(category)) {
    // Uncheck: remove category and all its services
    newCats.delete(category)
    for (const svc of services) {
      newSvcs.delete(`${category}::${svc}`)
    }
  } else {
    // Check: add category, remove individual service selections (they're implied)
    newCats.add(category)
    for (const svc of services) {
      newSvcs.delete(`${category}::${svc}`)
    }
  }

  checkedCategories.value = newCats
  checkedServices.value = newSvcs
  debounceCount()
}

function toggleService(service: string, category: string): void {
  const newCats = new Set(checkedCategories.value)
  const newSvcs = new Set(checkedServices.value)
  const key = `${category}::${service}`
  const services = catalog.value[category] || []

  if (isServiceChecked(service, category)) {
    // Uncheck
    if (newCats.has(category)) {
      // Category was fully checked → remove from categories, add all OTHER services individually
      newCats.delete(category)
      for (const svc of services) {
        const k = `${category}::${svc}`
        if (svc !== service) {
          newSvcs.add(k)
        }
      }
    } else {
      newSvcs.delete(key)
    }
  } else {
    // Check
    if (newCats.has(category)) {
      // Already fully checked → nothing to do
      return
    }
    newSvcs.add(key)
    // If now all services are checked, convert to category
    if (services.every((s) => newSvcs.has(`${category}::${s}`))) {
      newCats.add(category)
      for (const svc of services) {
        newSvcs.delete(`${category}::${svc}`)
      }
    }
  }

  checkedCategories.value = newCats
  checkedServices.value = newSvcs
  debounceCount()
}

// ── Count fetching ──────────────────────────────────────────
function debounceCount(): void {
  if (debounceTimer) clearTimeout(debounceTimer)
  countLoading.value = true
  debounceTimer = setTimeout(() => {
    debounceTimer = null
    fetchCounts()
  }, 300)
}

async function fetchCounts(): Promise<void> {
  const selections = buildSelectionPayload()
  try {
    const resp = await userApi.post('/user/count-prefixes', selections)
    countData.value = resp.data
  } catch (err) {
    handleAuthError(err)
    countData.value = null
  } finally {
    countLoading.value = false
  }
}

function buildSelectionPayload() {
  const categories: { category: string; checked: boolean }[] = []
  const services: { category: string; service: string; checked: boolean }[] = []

  for (const cat of Object.keys(data.value?.catalog || {})) {
    categories.push({ category: cat, checked: checkedCategories.value.has(cat) })
    for (const svc of (data.value?.catalog?.[cat] || [])) {
      services.push({
        category: cat,
        service: svc,
        checked: checkedServices.value.has(`${cat}::${svc}`),
      })
    }
  }

  return { categories, services }
}

// ── Save ────────────────────────────────────────────────────
async function saveSelections(): Promise<void> {
  saving.value = true
  try {
    await userApi.post('/user/selections', buildSelectionPayload())
    // The just-saved selection is now the baseline delta_v4/delta_v6 should
    // be measured against — without this, the delta badge keeps showing
    // the pre-save delta as if it were still unsaved.
    await fetchCounts()
    toast.add({ severity: 'success', summary: t('user.saved'), life: 3000 })
  } catch (err) {
    if (handleAuthError(err)) return
    toast.add({ severity: 'error', summary: 'Error', life: 5000 })
  } finally {
    saving.value = false
  }
}

async function saveFilters(): Promise<void> {
  savingFilters.value = true
  try {
    const allow = filterAllow.value
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
    const deny = filterDeny.value
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
    await userApi.post('/user/filters', { allow, deny })
    toast.add({ severity: 'success', summary: t('user.filters_saved'), life: 3000 })
  } catch (err) {
    if (handleAuthError(err)) return
    toast.add({ severity: 'error', summary: 'Error', life: 5000 })
  } finally {
    savingFilters.value = false
  }
}

// ── Lifecycle ───────────────────────────────────────────────
onMounted(() => {
  checkAuth()
})
</script>

<template>
  <!-- Toast -->
  <Toast />

  <!-- Full-screen loading -->
  <div v-if="authChecking" class="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-950">
    <div class="text-gray-500 dark:text-gray-400 text-lg">{{ t('user.loading') }}</div>
  </div>

  <!-- Login view -->
  <div v-else-if="!authenticated" class="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-950 px-4">
    <div class="w-full max-w-sm">
      <div class="p-6 rounded-border shadow-sm bg-white dark:bg-gray-900">
        <div class="text-center mb-6">
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('user.title') }}</h1>
          <p class="text-gray-500 dark:text-gray-400 mt-1">{{ t('user.login_subtitle') }}</p>
        </div>

        <form @submit.prevent="handleLogin" class="flex flex-col gap-4">
          <div v-if="loginError" class="text-red-500 dark:text-red-400 text-sm text-center bg-red-50 dark:bg-red-900/20 rounded-lg py-2 px-3">
            {{ loginError }}
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('user.login') }}</label>
            <input
              v-model="loginForm.login"
              type="text"
              autocomplete="username"
              required
              class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent outline-none"
            >
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('login.password') }}</label>
            <input
              v-model="loginForm.password"
              type="password"
              autocomplete="current-password"
              required
              class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent outline-none"
            >
          </div>

          <button
            type="submit"
            :disabled="loginLoading"
            class="w-full py-2.5 px-4 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white font-medium rounded-lg transition-colors flex items-center justify-center gap-2"
          >
            <i v-if="loginLoading" class="pi pi-spin pi-spinner" />
            {{ t('user.login_button') }}
          </button>
        </form>
      </div>
    </div>
  </div>

  <!-- Main user view -->
  <div v-else class="min-h-screen bg-gray-50 dark:bg-gray-950">
    <!-- Sticky Header -->
    <header class="sticky top-0 z-10 bg-white dark:bg-gray-900 border-b border-gray-200 dark:border-gray-800 px-4 py-3">
      <div class="max-w-4xl mx-auto flex items-center justify-between">
        <h1 class="text-lg font-semibold text-gray-900 dark:text-white truncate flex-1 mr-3">{{ t('user.title') }}</h1>
        <span class="text-sm text-gray-600 dark:text-gray-400 mr-3 truncate" v-if="data">
          {{ data.user.name }}
        </span>
        <div class="flex items-center gap-3 flex-shrink-0">
          <LanguageSwitcher v-model="locale" />
          <button
            class="w-8 h-8 flex items-center justify-center rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
            @click="themeStore.toggleTheme()"
          >
            <i :class="themeStore.isDark ? 'pi pi-sun' : 'pi pi-moon'" class="text-gray-500 dark:text-gray-400" />
          </button>
          <button
            @click="handleLogout"
            class="text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 transition-colors"
          >
            {{ t('user.logout') }}
          </button>
        </div>
      </div>
    </header>

    <!-- Content -->
    <main class="max-w-4xl mx-auto px-4 py-6">
      <!-- Catalog mode switcher -->
      <div v-if="data?.user?.catalog_editable && data?.modes?.length" class="mb-6 flex items-center gap-3">
        <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('user.catalog_mode') }}:</label>
        <select
          :value="selectedModeId"
          @change="switchMode(Number(($event.target as HTMLSelectElement).value))"
          class="px-3 py-1.5 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-sm text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent outline-none"
        >
          <option v-for="mode in data.modes" :key="mode.id" :value="mode.id">{{ mode.name }}</option>
        </select>
      </div>

      <!-- Loading -->
      <div v-if="loading" class="flex justify-center py-12">
        <i class="pi pi-spin pi-spinner text-2xl text-gray-400 dark:text-gray-500" />
      </div>

      <template v-else-if="data">
        <!-- Catalog section -->
        <div class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
          <div v-if="!Object.keys(catalog).length" class="text-gray-400 dark:text-gray-500 text-center py-8">
            No catalog data available.
          </div>

          <div v-for="category in categoryNames" :key="category" class="mb-3 last:mb-0">
            <!-- Category row -->
            <div
              class="flex items-center gap-2 py-1.5 cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800/50 rounded px-2 -mx-2 group"
              :class="{ 'opacity-50': data.user.selection_locked }"
            >
              <div class="relative flex items-center">
                <div
                  v-if="isCategoryPartiallyChecked(category)"
                  class="w-5 h-5 rounded border-2 border-blue-500 bg-blue-500 flex items-center justify-center"
                >
                  <svg class="w-3 h-3 text-white" fill="none" stroke="currentColor" stroke-width="3" viewBox="0 0 24 24">
                    <line x1="5" y1="12" x2="19" y2="12" />
                  </svg>
                </div>
                <div
                  v-else
                  class="w-5 h-5 rounded border-2 flex items-center justify-center transition-colors"
                  :class="isCategoryFullyChecked(category) ? 'bg-blue-500 border-blue-500' : 'border-gray-300 dark:border-gray-600 group-hover:border-blue-400'"
                  @click.stop="!data.user.selection_locked && toggleCategory(category)"
                >
                  <svg
                    v-if="isCategoryFullyChecked(category)"
                    class="w-3 h-3 text-white"
                    fill="none"
                    stroke="currentColor"
                    stroke-width="3"
                    viewBox="0 0 24 24"
                  >
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                </div>
              </div>
              <span
                class="flex-1 text-sm font-medium text-gray-900 dark:text-white select-none"
                @click="!data.user.selection_locked && toggleCategory(category)"
              >
                {{ category }}
              </span>
              <span class="text-xs text-gray-400 dark:text-gray-500">
                (+{{ getCategoryCounts(category).v4 + getCategoryCounts(category).v6 }})
              </span>
            </div>

            <!-- Service rows -->
            <div
              v-for="service in catalog[category]"
              :key="service"
              class="flex items-center gap-2 py-1 ml-7 cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-800/50 rounded px-2 -mx-2 group"
              :class="{ 'opacity-50': data.user.selection_locked }"
            >
              <div
                class="w-5 h-5 rounded border-2 flex items-center justify-center transition-colors flex-shrink-0"
                :class="isServiceChecked(service, category) ? 'bg-blue-500 border-blue-500' : 'border-gray-300 dark:border-gray-600 group-hover:border-blue-400'"
                @click="!data.user.selection_locked && toggleService(service, category)"
              >
                <svg
                  v-if="isServiceChecked(service, category)"
                  class="w-3 h-3 text-white"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="3"
                  viewBox="0 0 24 24"
                >
                  <polyline points="20 6 9 17 4 12" />
                </svg>
              </div>
              <span
                class="flex-1 text-sm text-gray-700 dark:text-gray-300 select-none"
                @click="!data.user.selection_locked && toggleService(service, category)"
              >
                {{ service }}
              </span>
              <span class="text-xs text-gray-400 dark:text-gray-500">
                {{ getServiceCount(service, category).toLocaleString() }} {{ t('user.prefixes') }}
              </span>
            </div>
          </div>
        </div>

        <!-- Summary section -->
        <div class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
          <div class="flex items-center justify-between mb-3">
            <div class="flex items-center gap-1 text-sm text-gray-500 dark:text-gray-400">
              <span v-if="countLoading"><i class="pi pi-spin pi-spinner mr-1" /></span>
              <span>{{ t('user.ipv4') }}:</span>
              <span data-testid="total-v4" class="font-semibold text-gray-900 dark:text-white">{{ totalV4.toLocaleString() }} {{ t('user.prefixes') }}</span>
              <span class="mx-2">|</span>
              <span>{{ t('user.ipv6') }}:</span>
              <span data-testid="total-v6" class="font-semibold text-gray-900 dark:text-white">{{ totalV6.toLocaleString() }} {{ t('user.prefixes') }}</span>
            </div>
            <button
              data-testid="save-selections"
              @click="saveSelections"
              :disabled="saving || data.user.selection_locked"
              class="px-4 py-1.5 text-sm font-medium bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg transition-colors flex items-center gap-1.5"
            >
              <i v-if="saving" class="pi pi-spin pi-spinner" />
              {{ t('user.save') }}
            </button>
          </div>

          <!-- Delta badges -->
          <div v-if="countData && hasDelta" class="flex items-center gap-3 text-sm">
            <span v-if="countData.delta_v4 !== 0" :class="countData.delta_v4 > 0 ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'">
              {{ formatDelta(countData.delta_v4) }} {{ t('user.ipv4') }}
            </span>
            <span v-if="countData.delta_v6 !== 0" :class="countData.delta_v6 > 0 ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'">
              {{ formatDelta(countData.delta_v6) }} {{ t('user.ipv6') }}
            </span>
          </div>
        </div>

        <!-- Route Filters section -->
        <div v-if="data.user.filter_editable" class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            <FormField :label="t('user.filters_allow')" :hint="'user.filters_hint_allow'" input-id="ufallow">
              <Textarea id="ufallow" v-model="filterAllow" rows="3" fluid />
            </FormField>
            <FormField :label="t('user.filters_deny')" :hint="'user.filters_hint_deny'" input-id="ufdeny">
              <Textarea id="ufdeny" v-model="filterDeny" rows="3" fluid />
            </FormField>
          </div>
          <div class="flex justify-end mt-4">
            <button
              @click="saveFilters"
              :disabled="savingFilters"
              class="px-4 py-1.5 text-sm font-medium bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg transition-colors flex items-center gap-1.5"
            >
              <i v-if="savingFilters" class="pi pi-spin pi-spinner" />
              {{ t('user.save_filters') }}
            </button>
          </div>
        </div>
      </template>
    </main>
  </div>
</template>

