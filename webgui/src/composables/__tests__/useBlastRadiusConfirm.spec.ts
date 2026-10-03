import { describe, it, expect, vi } from 'vitest'
import { useBlastRadiusConfirm } from '../useBlastRadiusConfirm'
import type { BlastRadiusPreview } from '@/types/blast-radius'

const noImpact: BlastRadiusPreview = { affected_users: [], total_delta_v4: 0, total_delta_v6: 0 }
const withImpact: BlastRadiusPreview = {
  affected_users: [{ user_id: 1, name: 'u', before_v4: 1, before_v6: 0, after_v4: 0, after_v6: 0, lost_routes: true }],
  total_delta_v4: -1, total_delta_v6: 0,
}

describe('useBlastRadiusConfirm', () => {
  it('resolves true immediately and never opens the dialog when the preview has no impact', async () => {
    const { dialogVisible, preview, confirm } = useBlastRadiusConfirm()
    const fetchPreview = vi.fn().mockResolvedValue(noImpact)

    const ok = await confirm(fetchPreview)

    expect(ok).toBe(true)
    expect(dialogVisible.value).toBe(false)
    expect(preview.value).toBeNull()
  })

  it('opens the dialog and resolves true only once onApply fires', async () => {
    const { dialogVisible, preview, confirm, onApply } = useBlastRadiusConfirm()
    const fetchPreview = vi.fn().mockResolvedValue(withImpact)

    const pending = confirm(fetchPreview)
    // Let the fetchPreview promise settle before asserting the dialog opened.
    await new Promise((r) => setTimeout(r, 0))
    expect(dialogVisible.value).toBe(true)
    expect(preview.value).toEqual(withImpact)

    onApply()
    expect(dialogVisible.value).toBe(false)
    expect(await pending).toBe(true)
  })

  it('resolves false once onCancel fires, without calling the real save', async () => {
    const { dialogVisible, confirm, onCancel } = useBlastRadiusConfirm()
    const fetchPreview = vi.fn().mockResolvedValue(withImpact)

    const pending = confirm(fetchPreview)
    await new Promise((r) => setTimeout(r, 0))

    onCancel()
    expect(dialogVisible.value).toBe(false)
    expect(await pending).toBe(false)
  })

  it('rejects a second confirmation while one is pending, without disturbing the first', async () => {
    const { confirm, onApply } = useBlastRadiusConfirm()
    const first = confirm(vi.fn().mockResolvedValue(withImpact))
    await new Promise((r) => setTimeout(r, 0))

    expect(await confirm(vi.fn().mockResolvedValue(withImpact))).toBe(false)

    onApply()
    expect(await first).toBe(true)
  })
})
