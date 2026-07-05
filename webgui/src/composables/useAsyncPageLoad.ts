import { ref } from 'vue'

// Shared onMounted-load pattern for admin pages: shows a spinner while
// loading, and falls back to a reload prompt (ErrorPage.vue) instead of a
// permanent spinner or a silently-empty view if the load fails. Extracted
// after the same try/catch/loading/loadError wiring was pasted into five
// pages independently and a sixth (CommunitiesPage.vue) shipped without it,
// silently showing "no communities" on a load failure instead of any error.
export function useAsyncPageLoad() {
  const loading = ref(true)
  const loadError = ref(false)

  async function run(loader: () => Promise<void>): Promise<boolean> {
    loading.value = true
    loadError.value = false
    try {
      await loader()
      return true
    } catch {
      loadError.value = true
      return false
    } finally {
      loading.value = false
    }
  }

  return { loading, loadError, run }
}
