import { ref } from 'vue'
import type { BlastRadiusPreview } from '@/types/blast-radius'
import { hasBlastRadiusImpact } from '@/types/blast-radius'

// Shared gate for an admin save action that should preview its blast radius
// first: call confirm(fetchPreview) in place of the real save. It fetches
// the preview and, if it reports no actual change, resolves true straight
// away (matching CommunitiesPage.vue's existing "no changes -> just save"
// shortcut) — otherwise it opens BlastRadiusPreviewDialog.vue (via
// dialogVisible/preview) and waits for the admin's answer, resolving true
// only once onApply() fires, or false once onCancel()/onDialogCancel fires.
export function useBlastRadiusConfirm() {
  const dialogVisible = ref(false)
  const preview = ref<BlastRadiusPreview | null>(null)
  let resolveFn: ((apply: boolean) => void) | null = null

  async function confirm(fetchPreview: () => Promise<BlastRadiusPreview>): Promise<boolean> {
    const p = await fetchPreview()
    if (!hasBlastRadiusImpact(p)) return true
    preview.value = p
    dialogVisible.value = true
    return new Promise<boolean>((resolve) => {
      resolveFn = resolve
    })
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
