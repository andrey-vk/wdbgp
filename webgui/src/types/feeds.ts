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
  /**
   * When the last sync attempt finished (success or failure), RFC3339Nano,
   * since server start. Completion of a triggered sync is detected by this
   * value changing — unlike diffing last_error, that stays correct when a
   * failed retry rewrites the same error text.
   */
  sync_attempted_at?: string
}

export interface FeedsListResponse {
  feeds: Feed[]
}

/** 202 body of POST /admin/feeds/{id}/sync: pre-run completion baseline. */
export interface SyncAccepted {
  ok: boolean
  sync_attempted_at?: string
}

/** 202 body of POST /admin/feeds/sync-all: per-feed pre-run baselines. */
export interface SyncAllAccepted {
  ok: boolean
  baselines?: Record<number, string>
}
