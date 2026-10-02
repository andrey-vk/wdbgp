import type { Mode } from './modes'

export interface UserPublic {
  id: number
  name: string
  catalog_mode_id: number
  catalog_mode_name: string
  selection_locked: boolean
  filter_editable: boolean
  filter_override: boolean
  filter_mode: string
  catalog_editable: boolean
  networks: string[]
}

export type Catalog = Record<string, string[]>

export interface RouteFilters {
  allow: string[]
  deny: string[]
}

// A flat list, not a "category|service"-keyed map: a category legitimately
// containing "|" would collide with that key scheme (e.g. category "a"
// service "b" vs. group "a|b"), showing the wrong number next to one of the
// two. service is "" for a category (group)-level community.
export interface UserCommunity {
  category: string
  service: string
  community: number
}

export interface UserDataResponse {
  user: UserPublic
  catalog: Catalog
  selections: {
    categories: string[]
    services: Array<{ category: string; service: string }>
  }
  communities: UserCommunity[]
  prefix_counts: {
    v4: Record<string, Record<string, number>>
    v6: Record<string, Record<string, number>>
  }
  filters: RouteFilters
  modes: Mode[]
}

export interface LoginResponse {
  user: UserPublic
  catalog: Catalog
  selections: {
    categories: string[]
    services: Array<{ category: string; service: string }>
  }
  communities: UserCommunity[]
  filters: RouteFilters
  prefix_counts: {
    v4: Record<string, Record<string, number>>
    v6: Record<string, Record<string, number>>
  }
  modes: Mode[]
}

export interface LoginRequest {
  login: string
  password: string
}
