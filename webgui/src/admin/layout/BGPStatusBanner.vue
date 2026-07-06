<script setup lang="ts">
import { onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Message from 'primevue/message'
import Button from 'primevue/button'
import { useBGPStatusStore } from '@/admin/stores/bgpStatus'

const { t } = useI18n()
const toast = useToast()
const status = useBGPStatusStore()

const POLL_INTERVAL_MS = 20000
let pollTimer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  status.fetchStatus()
  pollTimer = setInterval(() => status.fetchStatus(), POLL_INTERVAL_MS)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})

async function apply() {
  const result = await status.reload()
  if (result.ok) {
    toast.add({ severity: 'success', summary: t('bgp_status.reload_success'), life: 3000 })
  } else {
    toast.add({
      severity: 'error',
      summary: t('bgp_status.reload_error'),
      detail: result.error ?? undefined,
      life: 6000,
    })
  }
}
</script>

<template>
  <Message
    v-if="!status.running"
    severity="error"
    :closable="false"
    data-testid="bgp-banner-failed"
  >
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <div>
        <div class="font-semibold">{{ t('bgp_status.failed_title') }}</div>
        <div
          v-if="status.lastError"
          class="text-sm opacity-90"
        >
          {{ status.lastError }}
        </div>
      </div>
      <Button
        :label="t('bgp_status.retry')"
        size="small"
        severity="danger"
        :loading="status.reloading"
        data-testid="bgp-banner-action"
        @click="apply"
      />
    </div>
  </Message>
  <Message
    v-else-if="status.restartPending"
    severity="info"
    :closable="false"
    data-testid="bgp-banner-pending"
  >
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <div>
        <div class="font-semibold">{{ t('bgp_status.pending_title') }}</div>
        <div class="text-sm opacity-90">{{ t('bgp_status.pending_body') }}</div>
      </div>
      <Button
        :label="t('bgp_status.apply_now')"
        size="small"
        :loading="status.reloading"
        data-testid="bgp-banner-action"
        @click="apply"
      />
    </div>
  </Message>
</template>
