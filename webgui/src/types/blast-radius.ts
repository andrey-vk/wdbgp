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
}

export interface BlastRadiusPreview {
  affected_users: AffectedUser[]
  total_delta_v4: number
  total_delta_v6: number
}

// The backend's affected-user sets are membership-based (e.g. "everyone on
// this mode", "everyone whose filter_mode uses the global filter") — most of
// those users commonly see no change at all from a given edit. Narrowing to
// only the users whose count actually moved is what decides whether showing
// a preview dialog is worth interrupting the save for at all, matching
// CommunitiesPage.vue's existing "no changes -> just save" shortcut.
export function changedUsers(preview: BlastRadiusPreview): AffectedUser[] {
  return preview.affected_users.filter(
    (u) => u.before_v4 !== u.after_v4 || u.before_v6 !== u.after_v6,
  )
}

export function hasBlastRadiusImpact(preview: BlastRadiusPreview): boolean {
  return changedUsers(preview).length > 0
}
