// Mirrors internal/store/blastradius.go's AffectedUser/BlastRadiusPreview —
// the JSON shape every /preview endpoint (settings filters, user route
// filters, mode feed membership, user mode move) returns.
export interface AffectedUser {
  user_id: number
  name: string
  before_v4: number
  before_v6: number
  after_v4: number
  after_v6: number
  lost_routes: boolean
  changed: boolean
}

export interface BlastRadiusPreview {
  affected_users: AffectedUser[]
  total_delta_v4: number
  total_delta_v6: number
}

// The backend's affected-user sets are membership-based (e.g. "everyone on
// this mode"), so most users commonly see no change. `changed` is the
// backend's comparison of the announced prefix sets themselves — not just
// counts, which can stay equal while the routes differ — and decides whether
// the preview dialog is worth interrupting the save for at all.
export function changedUsers(preview: BlastRadiusPreview): AffectedUser[] {
  return preview.affected_users.filter((u) => u.changed)
}

export function hasBlastRadiusImpact(preview: BlastRadiusPreview): boolean {
  return changedUsers(preview).length > 0
}
