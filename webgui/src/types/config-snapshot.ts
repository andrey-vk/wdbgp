// Mirrors internal/store/config_snapshot.go — the whole admin configuration
// (users, global route filters, communities, and modes) as one exportable,
// diffable, importable document. Feeds and credentials are out of scope by
// design: see ConfigUser's own comment there for why a password never
// appears here.

export interface RouteFilters {
  allow: string[]
  deny: string[]
}

export interface ConfigModeFeedLink {
  feed: string
  exclude: boolean
}

export interface ConfigCommunity {
  category: string
  service?: string
  community: number
}

export interface ConfigMode {
  name: string
  enabled: boolean
  feeds: ConfigModeFeedLink[]
  communities: ConfigCommunity[]
}

export interface ServiceKey {
  category: string
  service: string
}

export interface ConfigUser {
  name: string
  peer_ip: string
  peer_asn: number
  next_hop?: string
  networks: string[]
  enabled: boolean
  selection_locked: boolean
  filter_mode: string
  filter_editable: boolean
  catalog_mode: string
  catalog_mode_editable: boolean
  active_dial: boolean
  web_auth: string
  has_bgp_password: boolean
  route_filters: RouteFilters
  selected_categories: string[]
  selected_services: ServiceKey[]
}

export interface ConfigSnapshot {
  schema_version: number
  generated_at: number
  global_filters: RouteFilters
  modes: ConfigMode[]
  users: ConfigUser[]
}

export interface ConfigChangedEntity<T> {
  name: string
  before: T
  after: T
}

export interface ConfigEntityDiff<T> {
  added: T[]
  removed: T[]
  changed: ConfigChangedEntity<T>[]
}

export interface ConfigDiff {
  global_filters_changed: boolean
  global_filters_before: RouteFilters
  global_filters_after: RouteFilters
  modes: ConfigEntityDiff<ConfigMode>
  users: ConfigEntityDiff<ConfigUser>
}

export interface ConfigApplyResult {
  modes_created: string[] | null
  modes_updated: string[] | null
  users_created: string[] | null
  users_updated: string[] | null
  unknown_feeds: string[] | null
  unknown_modes: string[] | null
}

export interface ConfigImportPreviewResponse {
  diff: ConfigDiff
  digest: string
}

export interface ConfigImportResponse {
  result: ConfigApplyResult
  global_filters_applied: boolean
}
