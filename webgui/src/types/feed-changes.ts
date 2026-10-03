// Mirrors internal/store/feed_sync_changes.go — what a feed sync changed,
// and the user-facing note built from it.
export interface FeedSyncCategory {
  category: string
  added_services: number
}

export interface FeedSyncChange {
  synced_at: number
  added_services: number
  removed_services: number
  added_prefixes: number
  removed_prefixes: number
  categories: FeedSyncCategory[]
}

export interface UserFeedChange {
  feed_name: string
  synced_at: number
  categories: FeedSyncCategory[]
}
