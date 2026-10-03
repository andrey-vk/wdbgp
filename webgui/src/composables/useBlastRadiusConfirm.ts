import { ref } from 'vue'
import type { BlastRadiusPreview } from '@/types/blast-radius'
import { hasBlastRadiusImpact } from '@/types/blast-radius'

// Shared gate for an admin save action that should preview its blast radius
// first: call confirm(fetchPreview) in place of the real save. It fetches
// the preview and, if it reports no actual change, resolves true straight
// away (matching CommunitiesPage.vue's existing "no changes -> just save"
// shortcut) — otherwise it opens BlastRadiusPreviewDialog.vue (via
// dialogVisible/preview) and waits for the admin's answer, resolving true
// only once onApply() fires, or false once onCancel() fires.
export function useBlastRadiusConfirm() {
  const dialogVisible = ref(false)
  const preview = ref<BlastRadiusPreview | null>(null)
  let resolveFn: ((apply: boolean) => void) | null = null
  // One confirmation at a time. A second caller while one is pending (a
  // repeated Save or header click during the preview request) gets false
  // straight away instead of overwriting resolveFn and leaving the first
  // caller's promise stranded.
  let pending = false

  async function confirm(fetchPreview: () => Promise<BlastRadiusPreview>): Promise<boolean> {
    if (pending) return false
    pending = true
    try {
      const p = await fetchPreview()
      if (!hasBlastRadiusImpact(p)) return true
      preview.value = p
      dialogVisible.value = true
      return await new Promise<boolean>((resolve) => {
        resolveFn = resolve
      })
    } finally {
      pending = false
    }
  }

  function onApply() {
    dialogVisible.value = false
    resolveFn?.(true)
    resolveFn = null
  }

  function onCancel() {
    dialogVisible.value = false
    resolveFn?.(false)
    resolveFn = null
  }

  return { dialogVisible, preview, confirm, onApply, onCancel }
}
