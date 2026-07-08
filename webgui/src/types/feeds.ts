export interface Feed {
  id: number
  name: string
  url: string
  enabled: boolean
  sync_interval: number
  adapter_id: number
  allowed_hosts: string
  restrict_hosts: boolean
  data?: string
  last_success?: string
  last_error?: string
  /** Live status: a sync for this feed is currently running (list responses). */
  syncing: boolean
  sync_started_at?: string
}

export interface FeedsListResponse {
  feeds: Feed[]
}
