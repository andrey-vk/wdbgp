// Mirrors internal/store/community_matrix.go — each category's group-level
// community in every mode. A null value means no group-level community there.
export interface CommunityMatrixMode {
  id: number
  name: string
  enabled: boolean
}

export interface CommunityMatrixRow {
  category: string
  values: Record<string, number | null>
  divergent: boolean
}

export interface CommunityMatrixResponse {
  modes: CommunityMatrixMode[]
  categories: CommunityMatrixRow[]
}
