export interface Mode {
  id: number
  key: string
  name: string
  enabled: boolean
  feed_count: number
}

export interface ModeFeedItem {
  id: number
  name: string
  url: string
  enabled: boolean
  adapter_name: string
}

export interface ModesListResponse {
  modes: Mode[]
}

export interface ModeDetailResponse {
  mode: Mode
  feeds: ModeFeedItem[]
}
