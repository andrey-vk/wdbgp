import { describe, it, expect } from 'vitest'
import { changedUsers, hasBlastRadiusImpact } from '../blast-radius'
import type { BlastRadiusPreview } from '../blast-radius'

function user(over: Partial<BlastRadiusPreview['affected_users'][number]> = {}) {
  return {
    user_id: 1, name: 'u', before_v4: 1, before_v6: 0, after_v4: 1, after_v6: 0, lost_routes: false,
    ...over,
  }
}

describe('changedUsers / hasBlastRadiusImpact', () => {
  it('excludes a user whose before/after counts are identical', () => {
    const preview: BlastRadiusPreview = {
      affected_users: [user()],
      total_delta_v4: 0, total_delta_v6: 0,
    }
    expect(changedUsers(preview)).toEqual([])
    expect(hasBlastRadiusImpact(preview)).toBe(false)
  })

  it('includes a user whose v4 count moved', () => {
    const changed = user({ user_id: 2, after_v4: 0, lost_routes: true })
    const preview: BlastRadiusPreview = {
      affected_users: [user(), changed],
      total_delta_v4: -1, total_delta_v6: 0,
    }
    expect(changedUsers(preview)).toEqual([changed])
    expect(hasBlastRadiusImpact(preview)).toBe(true)
  })

  it('includes a user whose v6 count moved even with v4 unchanged', () => {
    const changed = user({ user_id: 3, after_v6: 2 })
    const preview: BlastRadiusPreview = {
      affected_users: [changed],
      total_delta_v4: 0, total_delta_v6: 2,
    }
    expect(hasBlastRadiusImpact(preview)).toBe(true)
  })

  it('reports no impact for an empty affected-user list', () => {
    expect(hasBlastRadiusImpact({ affected_users: [], total_delta_v4: 0, total_delta_v6: 0 })).toBe(false)
  })
})
