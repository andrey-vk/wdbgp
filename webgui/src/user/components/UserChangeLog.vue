<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { UserChangeEntry, UserChangeList } from '@/types/change-log'

defineProps<{ entries: UserChangeEntry[] }>()

const { t } = useI18n()

function when(at: number): string {
  return new Date(at * 1000).toLocaleString()
}

function who(e: UserChangeEntry): string {
  if (e.source === 'self') return t('user.change_log_by_self')
  if (e.source === 'feed_sync') return t('user.change_log_by_feed', { feed: e.feed_name ?? '' })
  return t('user.change_log_by_admin')
}

function filterMode(value: string | undefined): string {
  switch (value) {
    case 'global':
      return t('user.change_log_fm_global')
    case 'extend':
      return t('user.change_log_fm_extend')
    case 'override':
      return t('user.change_log_fm_override')
    default:
      return value ?? ''
  }
}

function header(e: UserChangeEntry): string {
  switch (e.kind) {
    case 'selections':
      return t('user.change_log_selections', { mode: e.mode ?? '' })
    case 'feed_sync':
      return t('user.change_log_feed_sync', { mode: e.mode ?? '' })
    case 'mode':
      return t('user.change_log_mode_moved', { from: e.from ?? '', to: e.to ?? '' })
    case 'filter_mode':
      return t('user.change_log_filter_mode', { from: filterMode(e.from), to: filterMode(e.to) })
    default:
      return t('user.change_log_routes')
  }
}

function items(list: UserChangeList): string {
  return [
    ...list.categories,
    ...list.services.map((s) => `${s.category} / ${s.service}`),
    ...list.routes,
  ].join(', ')
}

function hasItems(list: UserChangeList): boolean {
  return list.categories.length + list.services.length + list.routes.length > 0 || list.omitted > 0
}

// One "Added: …" or "Removed: …" line, with any items past the list cap noted
// in the same text, so the sentence reads as one piece.
function sideLine(label: 'user.change_log_added' | 'user.change_log_removed', list: UserChangeList): string {
  const line = t(label, { items: items(list) })
  return list.omitted > 0 ? line + t('user.change_log_omitted', { count: list.omitted }) : line
}
</script>

<template>
  <div data-testid="change-log-section" class="p-6 rounded-border shadow-sm mb-6 bg-white dark:bg-gray-900">
    <h2 class="text-sm font-semibold text-gray-900 dark:text-white mb-3">{{ t('user.change_log_title') }}</h2>
    <p v-if="entries.length === 0" data-testid="change-log-empty" class="text-sm text-gray-500 dark:text-gray-400">
      {{ t('user.change_log_empty') }}
    </p>
    <ul v-else class="space-y-3">
      <li v-for="(e, i) in entries" :key="i" data-testid="change-log-entry" class="text-sm">
        <div class="text-gray-500 dark:text-gray-400">{{ when(e.at) }} · {{ who(e) }}</div>
        <div class="text-gray-900 dark:text-gray-100">{{ header(e) }}</div>
        <div v-if="hasItems(e.added)" class="pl-3 text-gray-700 dark:text-gray-300">{{ sideLine('user.change_log_added', e.added) }}</div>
        <div v-if="hasItems(e.removed)" class="pl-3 text-gray-700 dark:text-gray-300">{{ sideLine('user.change_log_removed', e.removed) }}</div>
      </li>
    </ul>
    <p class="text-xs text-gray-500 dark:text-gray-400 mt-3">{{ t('user.change_log_hint') }}</p>
  </div>
</template>
