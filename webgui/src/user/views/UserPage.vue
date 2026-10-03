<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import axios from 'axios'
import Textarea from 'primevue/textarea'
import FormField from '@/components/FormField.vue'
import LanguageSwitcher from '@/components/LanguageSwitcher.vue'
import { useThemeStore } from '@/admin/stores/theme'
import { getCurrentLocale } from '@/plugins/i18n'
import userApi from '@/api/client'
import { useSequencedRequest } from '@/composables/useSequencedRequest'
import type { UserDataResponse, UserRouteFiltersResult } from '@/types/user-page'
import type { UserFeedChange } from '@/types/feed-changes'
import type { UserCIDRLookupResult } from '@/types/user-debug'

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
const countRequest = useSequencedRequest()
const lookupRequest = useSequencedRequest()

// ── Save state ──────────────────────────────────────────────
const saving = ref(false)
const savingFilters = ref(false)

// ── Filter state ────────────────────────────────────────────
const filterAllow = ref('')
const filterDeny = ref('')

// ── Filters-in-effect state (read-only) ──────────────────────
const routeFiltersInfo = ref<UserRouteFiltersResult | null>(null)
const routeFiltersRequest = useSequencedRequest()
const feedChanges = ref<UserFeedChange[]>([])
const feedChangesRequest = useSequencedRequest()

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

// Keyed the same collision-free way as checkedServices: the backend sends a
// flat list rather than a "category|service"-joined map for exactly this
// reason (a category containing "|" could otherwise make this map show the
// wrong number next to one of two colliding entries).
const communityMap = computed<Map<string, number>>(() => {
  const map = new Map<string, number>()
  for (const c of data.value?.communities || []) {
    map.set(serviceKey(c.category, c.service), c.community)
  }
  return map
})

function getCategoryCommunity(category: string): number | undefined {
  return communityMap.value.get(serviceKey(category, ''))
}

function getServiceCommunity(service: string, category: string): number | undefined {
  return communityMap.value.get(serviceKey(category, service))
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

// JSON-encoded rather than joined with a separator: a category legitimately
// containing "::" could collide with an unrelated pair (e.g. category "a"
// service "b::c" vs. category "a::b" service "c"), corrupting checkedServices
// membership checks and silently flipping which pair gets saved as checked.
function serviceKey(category: string, service: string): string {
  return JSON.stringify([category, service])
}

function isServiceChecked(service: string, category: string): boolean {
  if (checkedCategories.value.has(category)) return true
  return checkedServices.value.has(serviceKey(category, service))
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

// cidrs is nullable: an empty RouteFilters.Allow/Deny is a nil slice on the
// Go side, which encoding/json serializes as null rather than [] — the same
// reason userData.filters?.allow is read with a `|| []` fallback elsewhere
// in this file.
function formatFilterList(cidrs: string[] | null | undefined): string {
  return cidrs?.length ? cidrs.join(', ') : t('user.route_filters_empty')
}

// ── Auth functions ──────────────────────────────────────────
// Clears every piece of per-user state that handleLogout and a 401-driven
// handleAuthError both need to reset. Without this shared by both, only
// explicit logout cleared it — a session that merely expired (any
// authenticated action can hit this) left the previous user's data,
// counts, and filters-in-effect sitting in memory, including a stale
// filters-in-effect response still in flight, for the next user who logs
// in on the same page to briefly (or indefinitely, if their own count
// fetch stalls) see.
function resetSessionState(): void {
  data.value = null
  countData.value = null
  checkedCategories.value = new Set()
  checkedServices.value = new Set()
  invalidateRouteFiltersInfo()
  invalidateFeedChanges()
  lookupQuery.value = ''
  invalidateLookup()
}

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
    resetSessionState()
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
  resetSessionState()
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
    svcSet.add(serviceKey(svc.category, svc.service))
  }

  checkedCategories.value = catSet
  checkedServices.value = svcSet

  // Initialize filter text
  filterAllow.value = (userData.filters?.allow || []).join('\n')
  filterDeny.value = (userData.filters?.deny || []).join('\n')

  // A previous (or still in-flight) lookup describes the catalog mode and
  // selections at the time it ran. This function reloads both (called
  // after a mode switch, on login, and on initial auth check), so any
  // prior or pending answer may no longer be accurate — invalidate it
  // rather than leave a stale verdict displayed, or let an in-flight
  // request that resolves afterward repopulate it, under a now-different
  // configuration.
  invalidateLookup()

  // Fetch live counts
  await fetchCounts()
  await fetchRouteFiltersInfo()
  await fetchFeedChanges()
}

// Bumps routeFiltersRequest's sequence so a GET still in flight at logout
// can't land in the next user's session: loadUserData awaits fetchCounts()
// before starting that user's own fetchRouteFiltersInfo call, and until
// that call starts nothing else advances the sequence — so a stale
// response arriving during that wait (e.g. because the next user's own
// count fetch is slow) would otherwise still read as "current" and
// restore the previous user's filters into their view, indefinitely if
// that count fetch stalls.
function invalidateRouteFiltersInfo(): void {
  routeFiltersRequest.next()
  routeFiltersInfo.value = null
}

function invalidateFeedChanges(): void {
  feedChangesRequest.next()
  feedChanges.value = []
}

async function fetchFeedChanges(): Promise<void> {
  const token = feedChangesRequest.next()
  try {
    const resp = await userApi.get<{ changes: UserFeedChange[] }>('/user/feed-changes')
    if (!feedChangesRequest.isCurrent(token)) return
    feedChanges.value = resp.data.changes
  } catch (err) {
    if (!feedChangesRequest.isCurrent(token)) return
    if (handleAuthError(err)) return
    feedChanges.value = []
  }
}

// Acknowledges what the user was shown: everything up to the newest change in
// the list (by change ID, so changes that share a second stay distinct). Changes that land after this page loaded stay unseen.
async function dismissFeedChanges(): Promise<void> {
  const shown = feedChanges.value
  if (shown.length === 0) return
  const through = Math.max(...shown.map((c) => c.change_id))
  // The mode is the one these notices were shown for, so an acknowledgement
  // can't move a different mode's cursor after a mode switch.
  const mode_id = shown[0].mode_id
  // Drop any fetch still in flight: its response may still hold the rows being
  // acknowledged, and would bring them back after the dismissal.
  feedChangesRequest.next()
  feedChanges.value = []
  try {
    await userApi.post('/user/feed-changes/ack', { mode_id, through })
  } catch (err) {
    if (handleAuthError(err)) return
    // Resync from the server rather than restoring what was shown.
    await fetchFeedChanges()
  }
}

// Filters in effect don't depend on catalog mode or selections, but
// loadUserData already re-runs on login/mode-switch/initial-auth, so
// refetching here is the simplest correct place for a cheap, idempotent GET.
// Sequenced like fetchCounts: if a newer fetch starts (a mode switch, a
// save) before this one resolves, the token guard below drops the stale
// response instead of letting it overwrite the newer one's data.
async function fetchRouteFiltersInfo(): Promise<void> {
  const token = routeFiltersRequest.next()
  try {
    const resp = await userApi.get('/user/route-filters')
    if (!routeFiltersRequest.isCurrent(token)) return
    routeFiltersInfo.value = resp.data
  } catch (err) {
    if (!routeFiltersRequest.isCurrent(token)) return
    if (handleAuthError(err)) return
    routeFiltersInfo.value = null
  }
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
    await invalidatingLookup(userApi.put('/user/mode', { mode_id: modeId }))
    selectedModeId.value = modeId
    await reloadUserData()
    toast.add({ severity: 'success', summary: t('user.saved'), life: 3000 })
  } catch (err) {
    if (handleAuthError(err)) return
    // The backend commits the mode change before it can fail on a later
    // step (e.g. BGP reconciliation), so an error here doesn't mean the
    // switch didn't happen — resync from the server's true state rather
    // than leaving the UI showing pre-switch mode/catalog/selections.
    toast.add({ severity: 'error', summary: t('user.save_error'), life: 5000 })
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
      newSvcs.delete(serviceKey(category, svc))
    }
  } else {
    // Check: add category, remove individual service selections (they're implied)
    newCats.add(category)
    for (const svc of services) {
      newSvcs.delete(serviceKey(category, svc))
    }
  }

  checkedCategories.value = newCats
  checkedServices.value = newSvcs
  debounceCount()
}

function toggleService(service: string, category: string): void {
  const newCats = new Set(checkedCategories.value)
  const newSvcs = new Set(checkedServices.value)
  const key = serviceKey(category, service)
  const services = catalog.value[category] || []

  if (isServiceChecked(service, category)) {
    // Uncheck
    if (newCats.has(category)) {
      // Category was fully checked → remove from categories, add all OTHER services individually
      newCats.delete(category)
      for (const svc of services) {
        const k = serviceKey(category, svc)
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
    if (services.every((s) => newSvcs.has(serviceKey(category, s)))) {
      newCats.add(category)
      for (const svc of services) {
        newSvcs.delete(serviceKey(category, svc))
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
  const token = countRequest.next()
  const selections = buildSelectionPayload()
  try {
    const resp = await userApi.post('/user/count-prefixes', selections)
    if (!countRequest.isCurrent(token)) return // a newer request superseded this one
    countData.value = resp.data
  } catch (err) {
    if (!countRequest.isCurrent(token)) return
    handleAuthError(err)
    countData.value = null
  } finally {
    if (countRequest.isCurrent(token)) countLoading.value = false
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
        checked: checkedServices.value.has(serviceKey(cat, svc)),
      })
    }
  }

  return { categories, services }
}

// ── Save ────────────────────────────────────────────────────
async function saveSelections(): Promise<void> {
  saving.value = true
  try {
    await invalidatingLookup(userApi.post('/user/selections', buildSelectionPayload()))
    // The just-saved selection is now the baseline delta_v4/delta_v6 should
    // be measured against — without this, the delta badge keeps showing
    // the pre-save delta as if it were still unsaved.
    await fetchCounts()
    // The notice lists prefixes announced through selected categories, so a
    // changed selection changes what it should show.
    await fetchFeedChanges()
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
    await invalidatingLookup(userApi.post('/user/filters', { allow, deny }))
    toast.add({ severity: 'success', summary: t('user.filters_saved'), life: 3000 })
    // The "filters in effect" section reads the user's own allow/deny lists
    // too (in extend/override mode) — without this it keeps showing the
    // pre-save lists until the next mode switch or page reload.
    await fetchRouteFiltersInfo()
    // Route filters decide which of the notice's prefixes are announced.
    await fetchFeedChanges()
  } catch (err) {
    if (handleAuthError(err)) return
    toast.add({ severity: 'error', summary: 'Error', life: 5000 })
  } finally {
    savingFilters.value = false
  }
}

// ── Address lookup state ───────────────────────────────────
const lookupQuery = ref('')
const lookupLoading = ref(false)
const lookupError = ref('')
const lookupResult = ref<UserCIDRLookupResult | null>(null)

// A query can be a CIDR block only partly covered by what's actually
// delivered (e.g. a /24 with one selected /25) — after_percentage can
// legitimately land strictly between 0 and 100, and collapsing that to
// the same "in your tunnel" verdict as a fully-delivered /32 would imply
// the whole queried range is routed when only part of it is.
const lookupVerdict = computed<'none' | 'partial' | 'full'>(() => {
  const after = lookupResult.value?.after_percentage ?? 0
  if (after <= 0) return 'none'
  if (after >= 100) return 'full'
  return 'partial'
})
// Math.round alone can round a genuinely-partial value to a boundary that
// contradicts the partial verdict next to it (99.6% rounding to "100%", or
// 0.4% rounding to "0%") — bound it instead so the number shown can never
// read as "none" or "full" when the verdict says otherwise.
function formatLookupPercentage(pct: number): string {
  if (pct > 0 && pct < 1) return '<1%'
  if (pct > 99 && pct < 100) return '>99%'
  return Math.round(pct) + '%'
}
const lookupVerdictText = computed(() => {
  switch (lookupVerdict.value) {
    case 'full':
      return t('user.lookup_in_tunnel')
    case 'partial':
      return t('user.lookup_partially_in_tunnel', {
        pct: formatLookupPercentage(lookupResult.value?.after_percentage ?? 0),
      })
    default:
      return t('user.lookup_not_in_tunnel')
  }
})
const lookupVerdictClass = computed(() => {
  switch (lookupVerdict.value) {
    case 'full':
      return 'text-green-600 dark:text-green-400'
    case 'partial':
      return 'text-amber-600 dark:text-amber-400'
    default:
      return 'text-red-600 dark:text-red-400'
  }
})

// Bumps the sequence so any in-flight lookup response is no longer
// current, then clears the displayed state. Used whenever something other
// than a fresh runLookup invalidates the previous answer (editing the
// query, or a mode switch/selection save/filter save/logout succeeding) —
// without bumping the sequence here, a request that was already in flight
// when one of those happened would still pass its own isCurrent check when
// it resolves, silently repopulating the state this just cleared.
function invalidateLookup(): void {
  lookupRequest.next()
  lookupLoading.value = false
  lookupError.value = ''
  lookupResult.value = null
}

// Invalidates the lookup the instant `request` settles — success or
// failure, before anything awaited after it at the call site. Several of
// the requests this wraps (save selections, save filters, switch mode)
// commit on the backend before they can fail on a later step (BGP
// reconciliation), so even a rejected request doesn't mean nothing
// changed; and invalidating in a `finally` here, rather than duplicated in
// a try block and its catch block at every call site, means a mutating
// request can't be added later without its invalidation, and an await
// placed after this one (a count refresh, a resync) can never delay or
// skip it, however slow or how it resolves.
async function invalidatingLookup<T>(request: Promise<T>): Promise<T> {
  try {
    return await request
  } finally {
    invalidateLookup()
  }
}

// The result panel shows lookupResult, not lookupQuery — editing the input
// without resubmitting (or a request resolving after the input changed
// again) must not leave an old query's answer displayed under a different,
// unsubmitted query string.
watch(lookupQuery, () => invalidateLookup())

async function runLookup(): Promise<void> {
  if (!lookupQuery.value.trim()) return
  // The button disables while loading, but Enter in the input can still
  // fire a second request after editing the query mid-flight — sequence
  // responses so a slower, now-superseded request can never overwrite a
  // newer one's result, matching fetchCounts' countRequest pattern.
  const token = lookupRequest.next()
  lookupLoading.value = true
  lookupError.value = ''
  lookupResult.value = null
  try {
    const resp = await userApi.get('/user/debug', { params: { cidr: lookupQuery.value.trim() } })
    if (!lookupRequest.isCurrent(token)) return
    lookupResult.value = resp.data
  } catch (err) {
    if (!lookupRequest.isCurrent(token)) return
    if (handleAuthError(err)) return
    const e = err as { response?: { data?: { error?: string } } }
    lookupError.value = e.response?.data?.error || t('user.lookup_error')
  } finally {
    if (lookupRequest.isCurrent(token)) lookupLoading.value = false
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
            data-testid="logout-button"
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
              <span
                v-if="getCategoryCommunity(category)"
                :title="t('user.community_group')"
                class="text-xs font-mono px-1.5 py-0.5 rounded bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-300"
              >
                {{ getCategoryCommunity(category) }}
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
              <span
                v-if="getServiceCommunity(service, category)"
                :title="t('user.community_service')"
                class="text-xs font-mono px-1.5 py-0.5 rounded bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-300"
              >
                {{ getServiceCommunity(service, category) }}
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

        <!-- Feed syncs that added services to categories you selected -->
        <div v-if="feedChanges.length" data-testid="feed-changes-section" class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
          <div class="flex items-center justify-between mb-3">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('user.feed_changes_title') }}</h2>
            <button type="button" data-testid="feed-changes-dismiss" class="text-sm text-blue-600 dark:text-blue-400" @click="dismissFeedChanges">{{ t('user.feed_changes_dismiss') }}</button>
          </div>
          <div v-for="change in feedChanges" :key="change.change_id" class="text-sm mb-2">
            <span class="text-gray-500 dark:text-gray-400">{{ new Date(change.synced_at * 1000).toLocaleString() }} · {{ change.feed_name }}</span>
            <div v-for="cat in change.categories" :key="cat.category" class="pl-3 text-gray-700 dark:text-gray-300">
              {{ t('user.feed_changes_line', { category: cat.category, added: cat.added_services }) }}
            </div>
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400 mt-2">{{ t('user.feed_changes_hint') }}</p>
        </div>

        <!-- Filters in effect (read-only) -->
        <div v-if="routeFiltersInfo" data-testid="route-filters-section" class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white mb-3">{{ t('user.route_filters_title') }}</h2>
          <p data-testid="route-filters-mode" class="text-sm text-gray-600 dark:text-gray-400 mb-3">
            {{ t(`user.route_filters_mode_${routeFiltersInfo.mode}`) }}
          </p>

          <div v-if="routeFiltersInfo.mode === 'extend'" class="grid grid-cols-1 md:grid-cols-2 gap-4 mb-4">
            <div data-testid="route-filters-global">
              <h3 class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase mb-1">{{ t('user.route_filters_global') }}</h3>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('user.route_filters_allow') }}: {{ formatFilterList(routeFiltersInfo.global.allow) }}</p>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('user.route_filters_deny') }}: {{ formatFilterList(routeFiltersInfo.global.deny) }}</p>
            </div>
            <div data-testid="route-filters-own">
              <h3 class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase mb-1">{{ t('user.route_filters_own') }}</h3>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('user.route_filters_allow') }}: {{ formatFilterList(routeFiltersInfo.own.allow) }}</p>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('user.route_filters_deny') }}: {{ formatFilterList(routeFiltersInfo.own.deny) }}</p>
            </div>
          </div>

          <div data-testid="route-filters-effective" class="border-t border-gray-100 dark:border-gray-800 pt-3 text-sm">
            <p class="text-gray-700 dark:text-gray-300">{{ t('user.route_filters_allow') }}: {{ formatFilterList(routeFiltersInfo.effective.allow) }}</p>
            <p class="text-gray-700 dark:text-gray-300">{{ t('user.route_filters_deny') }}: {{ formatFilterList(routeFiltersInfo.effective.deny) }}</p>
          </div>
        </div>

        <!-- Route Filters section -->
        <div v-if="data.user.filter_editable" class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white mb-3">{{ t('user.filters_edit_title') }}</h2>
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

        <!-- Address lookup section -->
        <div class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white mb-3">{{ t('user.lookup_title') }}</h2>
          <div class="flex items-end gap-3">
            <div class="flex-1">
              <label class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">{{ t('user.lookup_cidr') }}</label>
              <input
                v-model="lookupQuery"
                type="text"
                data-testid="lookup-input"
                @keyup.enter="runLookup"
                class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 text-gray-900 dark:text-white focus:ring-2 focus:ring-blue-500 focus:border-transparent outline-none"
              >
              <p class="text-xs text-gray-400 dark:text-gray-500 mt-1">{{ t('user.lookup_hint') }}</p>
            </div>
            <button
              data-testid="lookup-button"
              @click="runLookup"
              :disabled="lookupLoading || !lookupQuery.trim()"
              class="px-4 py-2 text-sm font-medium bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg transition-colors flex items-center gap-1.5"
            >
              <i v-if="lookupLoading" class="pi pi-spin pi-spinner" />
              {{ t('user.lookup_button') }}
            </button>
          </div>

          <div v-if="lookupError" data-testid="lookup-error" class="mt-3 text-sm text-red-500 dark:text-red-400">
            {{ lookupError }}
          </div>

          <div v-else-if="lookupResult" data-testid="lookup-result" class="mt-4">
            <div v-if="!lookupResult.matches.length" class="text-sm text-gray-400 dark:text-gray-500">
              {{ t('user.lookup_no_match') }}
            </div>
            <template v-else>
              <div class="flex flex-col gap-1 mb-3">
                <div
                  v-for="(m, index) in lookupResult.matches"
                  :key="index"
                  class="flex items-center justify-between text-sm py-1"
                  data-testid="lookup-match"
                >
                  <span class="text-gray-700 dark:text-gray-300">{{ m.category }} / {{ m.service }}</span>
                  <span class="flex items-center gap-2">
                    <span
                      class="text-xs px-1.5 py-0.5 rounded"
                      :class="m.selected ? 'bg-blue-50 dark:bg-blue-900/30 text-blue-600 dark:text-blue-300' : 'bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400'"
                    >
                      {{ m.selected ? t('user.lookup_selected') : t('user.lookup_not_selected') }}
                    </span>
                    <span class="text-gray-400 dark:text-gray-500">{{ formatLookupPercentage(m.percentage) }}</span>
                  </span>
                </div>
              </div>
              <div class="text-sm text-gray-600 dark:text-gray-400 border-t border-gray-100 dark:border-gray-800 pt-3">
                <span data-testid="lookup-in-tunnel" :class="lookupVerdictClass">
                  {{ lookupVerdictText }}
                </span>
                <span v-if="lookupResult.before_percentage !== lookupResult.after_percentage" class="ml-2">
                  ({{ t('user.lookup_filtered_note') }})
                </span>
              </div>
            </template>
          </div>
        </div>
      </template>
    </main>
  </div>
</template>

