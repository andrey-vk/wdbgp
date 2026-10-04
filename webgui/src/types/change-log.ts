// Mirrors internal/store/user_change_log.go — one entry in a user's change
// history. Source says who made the change; from/to carry old and new values
// for mode and filter-mode changes; mode names the mode a selection or feed
// change applies to.
export interface ChangeServiceKey {
  category: string
  service: string
}

export interface UserChangeList {
  categories: string[]
  services: ChangeServiceKey[]
  routes: string[]
  omitted: number
}

export interface UserChangeEntry {
  at: number
  source: 'self' | 'admin' | 'feed_sync'
  kind: 'selections' | 'mode' | 'filter_mode' | 'route_filters' | 'feed_sync'
  mode?: string
  feed_name?: string
  from?: string
  to?: string
  added: UserChangeList
  removed: UserChangeList
}
