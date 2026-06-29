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

export interface UserDataResponse {
  user: UserPublic
  catalog: Catalog
  selections: {
    categories: string[]
    services: Array<{ category: string; service: string }>
  }
  communities: Record<string, number>
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
  filters: RouteFilters
}

export interface LoginRequest {
  login: string
  password: string
}
