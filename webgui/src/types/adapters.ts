export interface Adapter {
  id: number
  key: string
  name: string
  language: string
  api_version: number
  source: string
  revision: number
  builtin: boolean
  forked_from?: number
  forked_version?: number
  requires_review?: boolean
}

export interface AdaptersListResponse {
  adapters: Adapter[]
}

export interface AdapterCreateUpdateRequest {
  name: string
  source: string
}
