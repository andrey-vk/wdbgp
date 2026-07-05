import type { User } from './users'
import type { Mode } from './modes'

export interface ServiceKey {
  category: string
  service: string
}

export type Catalog = Record<string, string[]>

export type PrefixCountMap = Record<string, Record<string, number>>

export interface UserCatalogResponse {
  user: User
  catalog: Catalog
  selections: {
    categories: string[]
    services: ServiceKey[]
  }
  prefix_counts: {
    v4: PrefixCountMap
    v6: PrefixCountMap
  }
  modes: Mode[]
}

export interface SelectionCountResponse {
  v4: number
  v6: number
  delta_v4: number
  delta_v6: number
}

export interface SelectionCheckItem {
  category: string
  checked: boolean
}

export interface ServiceCheckItem {
  category: string
  service: string
  checked: boolean
}

export interface SelectionPayload {
  categories: SelectionCheckItem[]
  services: ServiceCheckItem[]
  mode_id: number
}
