<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ConfigDiff } from '@/types/config-snapshot'

const { t } = useI18n()
defineProps<{ diff: ConfigDiff }>()
</script>

<template>
  <div class="text-sm">
    <div
      v-if="
        !diff.global_filters_changed &&
          diff.modes.added.length === 0 && diff.modes.changed.length === 0 && diff.modes.removed.length === 0 &&
          diff.users.added.length === 0 && diff.users.changed.length === 0 && diff.users.removed.length === 0
      "
      class="text-gray-500 dark:text-gray-400 py-2"
    >
      {{ t('config.diff.no_changes') }}
    </div>

    <template v-else>
      <div v-if="diff.global_filters_changed" class="mb-3" data-testid="diff-global-filters">
        <div class="font-semibold mb-1">{{ t('config.diff.global_filters') }}</div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('config.diff.allow') }}</div>
            <div class="text-gray-500 dark:text-gray-400 line-through">{{ diff.global_filters_before.allow.join(', ') || '—' }}</div>
            <div class="font-medium">{{ diff.global_filters_after.allow.join(', ') || '—' }}</div>
          </div>
          <div>
            <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('config.diff.deny') }}</div>
            <div class="text-gray-500 dark:text-gray-400 line-through">{{ diff.global_filters_before.deny.join(', ') || '—' }}</div>
            <div class="font-medium">{{ diff.global_filters_after.deny.join(', ') || '—' }}</div>
          </div>
        </div>
      </div>

      <div v-for="section in [{ key: 'modes', label: t('config.diff.modes'), data: diff.modes }, { key: 'users', label: t('config.diff.users'), data: diff.users }]" :key="section.key" class="mb-3" :data-testid="`diff-${section.key}`">
        <div v-if="section.data.added.length || section.data.changed.length || section.data.removed.length" class="font-semibold mb-1">{{ section.label }}</div>
        <div v-if="section.data.added.length" class="mb-1">
          <span class="text-xs text-green-700 dark:text-green-400">{{ t('config.diff.added', { count: section.data.added.length }) }}:</span>
          <span class="ml-1">{{ section.data.added.map((e: { name: string }) => e.name).join(', ') }}</span>
        </div>
        <div v-if="section.data.changed.length" class="mb-1">
          <span class="text-xs text-amber-700 dark:text-amber-400">{{ t('config.diff.changed', { count: section.data.changed.length }) }}:</span>
          <span class="ml-1">{{ section.data.changed.map((e: { name: string }) => e.name).join(', ') }}</span>
        </div>
        <div v-if="section.data.removed.length">
          <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('config.diff.removed', { count: section.data.removed.length }) }}:</span>
          <span class="ml-1 text-gray-500 dark:text-gray-400">{{ section.data.removed.map((e: { name: string }) => e.name).join(', ') }}</span>
        </div>
      </div>
    </template>
  </div>
</template>
