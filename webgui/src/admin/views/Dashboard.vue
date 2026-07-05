<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import apiClient from '@/api/client'
import Chart from 'primevue/chart'
import Tag from 'primevue/tag'
import type { DashboardData, UserHistoryPoint, FeedHistoryPoint, FeedItem, ChartTooltipItem } from '@/types/dashboard'

const { t } = useI18n()
const loading = ref(true)
const data = ref<DashboardData | null>(null)

onMounted(async () => {
  const resp = await apiClient.get('/admin/dashboard')
  data.value = resp.data
  loading.value = false
  startStatusPolling()
})

onUnmounted(() => {
  stopStatusPolling()
})

// Stat cards data
const stats = computed(() => {
  if (!data.value) return []
  return [
    { label: t('dashboard.prefixes'), value: data.value.prefixes?.toLocaleString() || '0', icon: 'pi-globe', color: 'blue' },
    { label: t('dashboard.bgp_peers'), value: `${data.value.bgp?.connected_peers || 0}/${data.value.bgp?.total_peers || 0}`, icon: 'pi-sitemap', color: 'green' },
    { label: t('dashboard.feeds'), value: `${data.value.feeds?.enabled || 0}/${data.value.feeds?.total || 0}`, icon: 'pi-download', color: 'teal' },
    { label: t('dashboard.users'), value: data.value.users?.total || 0, icon: 'pi-users', color: 'purple' },
  ]
})

// User history chart (stacked area)
const userChartData = computed(() => {
  if (!data.value?.user_history?.length) return null
  const labels = data.value.user_history.map((h: UserHistoryPoint) => h.time.substring(0, 16).replace('T', ' '))
  return {
    labels,
    datasets: [
      { label: t('dashboard.users_disabled'), data: data.value.user_history.map((h: UserHistoryPoint) => h.disabled), backgroundColor: '#f59e0b', borderColor: '#f59e0b', fill: true, tension: 0, pointRadius: 0 },
      { label: t('dashboard.users_connected'), data: data.value.user_history.map((h: UserHistoryPoint) => h.connected), backgroundColor: '#10b981', borderColor: '#10b981', fill: true, tension: 0, pointRadius: 0 },
      { label: t('dashboard.users_disconnected'), data: data.value.user_history.map((h: UserHistoryPoint) => h.total - h.disabled - h.connected), backgroundColor: '#6b7280', borderColor: '#6b7280', fill: true, tension: 0, pointRadius: 0 },
    ],
  }
})

const userChartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  scales: { x: { stacked: true }, y: { stacked: true, beginAtZero: true, title: { display: true, text: 'Users' } } },
  plugins: {
    legend: { position: 'bottom' },
    tooltip: {
      callbacks: {
        footer: (items: ChartTooltipItem[]) => {
          const sum = items.reduce((s: number, item: ChartTooltipItem) => s + (item.raw || 0), 0)
          return 'Total: ' + sum
        },
      },
    },
  },
}

// Feed history chart (stacked area)
const feedChartData = computed(() => {
  const feedHistory = data.value?.feed_history
  if (!feedHistory?.length) return null
  // Collect all feed IDs
  const feedIds = new Set<number>()
  feedHistory.forEach((h: FeedHistoryPoint) => {
    if (h.prefixes) Object.keys(h.prefixes).forEach(id => feedIds.add(parseInt(id)))
  })
  const labels = feedHistory.map((h: FeedHistoryPoint) => h.time.substring(0, 16).replace('T', ' '))
  const feedNames = new Map<number, string>()
  if (data.value?.feeds?.items) {
    data.value.feeds.items.forEach((f: FeedItem) => feedNames.set(f.id, f.name))
  }
  const colors = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899', '#06b6d4', '#84cc16']
  const datasets = Array.from(feedIds).map((id, i) => ({
    label: feedNames.get(id) || 'Feed #' + id,
    data: feedHistory.map((h: FeedHistoryPoint) => h.prefixes?.[id] || 0),
    backgroundColor: colors[i % colors.length] + '80',
    borderColor: colors[i % colors.length],
    fill: true,
    tension: 0,
    pointRadius: 0,
  }))
  return { labels, datasets }
})

const feedChartOptions = {
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  scales: { x: { stacked: true }, y: { stacked: true, beginAtZero: true, title: { display: true, text: 'Prefixes' } } },
  plugins: {
    legend: { position: 'bottom' },
    tooltip: {
      bodyMaxHeight: 200,
      bodyOverflow: 'auto',
      callbacks: {
        footer: (items: ChartTooltipItem[]) => {
          const sum = items.reduce((s: number, item: ChartTooltipItem) => s + (item.raw || 0), 0)
          return 'Total: ' + sum
        },
      },
    },
  },
}

// BGP peers for table
const bgpPeers = computed(() => data.value?.bgp?.peers || [])

function peerStateSeverity(state: string) {
  if (state === 'ESTABLISHED') return 'success'
  if (state === 'CONNECT' || state === 'OPENSENT' || state === 'OPENCONFIRM') return 'warn'
  return 'info'
}

// Uptime formatting
const uptimeStr = computed(() => {
  if (!data.value) return ''
  const s = data.value.uptime_seconds || 0
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  const parts = []
  if (d > 0) parts.push(`${d}d`)
  if (h > 0) parts.push(`${h}h`)
  parts.push(`${m}m`)
  return parts.join(' ')
})

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
  if (!data.value) return
  try {
    const resp = await apiClient.get('/admin/users/statuses')
    const states: Record<string, string> = resp.data.peer_states || {}
    for (const peer of data.value.bgp.peers) {
      const key = peer.ip + ':' + peer.asn
      peer.state = states[key] || ''
    }
    data.value.bgp.connected_peers = data.value.bgp.peers.filter(p => p.state === 'ESTABLISHED').length
  } catch {
    // silent — best-effort polling
  }
}
</script>

<template>
  <div class="dashboard-page max-w-[1100px]">
    <h1 class="text-xl font-semibold mb-4">{{ t('menu.dashboard') }}</h1>

    <div v-if="loading" class="flex justify-center py-8">
      <i class="pi pi-spin pi-spinner text-2xl" />
    </div>

    <div v-else>
      <!-- Stat cards row -->
      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        <div v-for="stat in stats" :key="stat.label" class="card p-4">
          <div class="flex justify-between items-start">
            <div>
              <div class="text-gray-500 dark:text-gray-400 text-sm mb-1">{{ stat.label }}</div>
              <div class="text-2xl font-semibold">{{ stat.value }}</div>
            </div>
            <div :class="['w-10 h-10 rounded-lg flex items-center justify-center', 'bg-' + stat.color + '-100', 'dark:bg-' + stat.color + '-400/10']">
              <i :class="['pi', stat.icon, 'text-' + stat.color + '-500', 'text-xl']" />
            </div>
          </div>
        </div>
      </div>

      <!-- User history chart -->
      <div class="card p-4 mb-6">
        <h2 class="font-semibold mb-3">{{ t('dashboard.users_over_time') }}</h2>
        <div v-if="data?.metrics_enabled === false" class="text-gray-400 dark:text-gray-500 py-8 text-center">Metrics collection is disabled. Enable in Settings.</div>
        <div v-else-if="userChartData" class="h-[20rem] chart-container">
          <Chart type="line" :data="userChartData" :options="userChartOptions" class="h-full" />
        </div>
        <div v-else class="text-gray-400 dark:text-gray-500 py-8 text-center">{{ t('dashboard.no_history') }}</div>
      </div>

      <!-- Feed history chart -->
      <div class="card p-4 mb-6">
        <h2 class="font-semibold mb-3">{{ t('dashboard.prefixes_over_time') }}</h2>
        <div v-if="data?.metrics_enabled === false" class="text-gray-400 dark:text-gray-500 py-8 text-center">Metrics collection is disabled. Enable in Settings.</div>
        <div v-else-if="feedChartData" class="h-[20rem] chart-container">
          <Chart type="line" :data="feedChartData" :options="feedChartOptions" class="h-full" />
        </div>
        <div v-else class="text-gray-400 dark:text-gray-500 py-8 text-center">{{ t('dashboard.no_history') }}</div>
      </div>

      <!-- BGP peers + System info (two cards side by side) -->
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-6">
        <!-- BGP peers -->
        <div class="card p-4">
          <h2 class="font-semibold mb-3">{{ t('dashboard.peers') }}</h2>
          <div v-if="bgpPeers.length" class="divide-y divide-gray-100 dark:divide-gray-800">
            <div v-for="peer in bgpPeers" :key="peer.ip + ':' + peer.asn" class="flex items-center gap-3 py-2">
              <span class="flex-1 font-medium truncate">{{ peer.name }}</span>
              <code class="text-xs text-gray-500 dark:text-gray-400">{{ peer.ip }}:{{ peer.asn }}</code>
              <Tag v-if="peer.state" :value="peer.state" :severity="peerStateSeverity(peer.state)" />
              <span v-else class="text-gray-400 text-sm">&mdash;</span>
            </div>
          </div>
          <div v-else class="text-gray-400 dark:text-gray-500 py-4 text-center">{{ t('dashboard.no_peers') }}</div>
        </div>

        <!-- System info -->
        <div class="card p-4">
          <h2 class="font-semibold mb-3">{{ t('dashboard.system') }}</h2>
          <div class="space-y-2 text-sm">
            <div class="flex justify-between"><span class="text-gray-500 dark:text-gray-400">{{ t('dashboard.uptime') }}</span><span class="font-medium">{{ uptimeStr }}</span></div>
            <div class="flex justify-between"><span class="text-gray-500 dark:text-gray-400">{{ t('dashboard.total_prefixes') }}</span><span class="font-medium">{{ data?.prefixes?.toLocaleString() || 0 }}</span></div>
            <div class="flex justify-between"><span class="text-gray-500 dark:text-gray-400">{{ t('dashboard.categories') }}</span><span class="font-medium">{{ data?.categories || 0 }}</span></div>
            <div class="flex justify-between"><span class="text-gray-500 dark:text-gray-400">{{ t('dashboard.services') }}</span><span class="font-medium">{{ data?.services || 0 }}</span></div>
            <div class="flex justify-between"><span class="text-gray-500 dark:text-gray-400">{{ t('dashboard.modes') }}</span><span class="font-medium">{{ data?.modes?.length || 0 }}</span></div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Chart.js sets inline height/width on canvas — must override */
.chart-container :deep(canvas) {
  height: 100% !important;
}
</style>
