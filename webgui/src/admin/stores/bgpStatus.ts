import { defineStore } from 'pinia'
import { ref } from 'vue'
import apiClient from '@/api/client'

interface BGPStatusResponse {
  running: boolean
  restart_pending: boolean
  last_error?: string
}

export const useBGPStatusStore = defineStore('bgpStatus', () => {
  // Optimistic defaults so the banner stays hidden until the first fetch
  // resolves, rather than flashing an error state on load.
  const running = ref(true)
  const restartPending = ref(false)
  const lastError = ref<string | null>(null)
  const reloading = ref(false)

  function applyResponse(data: BGPStatusResponse) {
    running.value = data.running
    restartPending.value = data.restart_pending
    lastError.value = data.last_error ?? null
  }

  async function fetchStatus(): Promise<void> {
    try {
      const resp = await apiClient.get<BGPStatusResponse>('/admin/bgp/status')
      applyResponse(resp.data)
    } catch {
      // Best-effort — a failed poll shouldn't flip the banner to an
      // alarming state; just leave the last known status in place.
    }
  }

  async function reload(): Promise<{ ok: boolean; error: string | null }> {
    reloading.value = true
    try {
      const resp = await apiClient.post<BGPStatusResponse>('/admin/bgp/reload')
      applyResponse(resp.data)
      return { ok: true, error: null }
    } catch (err: unknown) {
      const e = err as { response?: { data?: BGPStatusResponse } }
      if (e.response?.data) {
        applyResponse(e.response.data)
        return { ok: false, error: e.response.data.last_error ?? null }
      }
      return { ok: false, error: null }
    } finally {
      reloading.value = false
    }
  }

  return { running, restartPending, lastError, reloading, fetchStatus, reload }
})
