export interface UserHistoryPoint {
  time: string
  disabled: number
  connected: number
  total: number
}

export interface FeedHistoryPoint {
  time: string
  prefixes?: Record<string, number>
}

export interface FeedItem {
  id: number
  name: string
  enabled: boolean
  last_error?: string
}

export interface DashboardData {
  prefixes: number
  categories: number
  services: number
  uptime_seconds: number
  metrics_enabled: boolean
  users: { total: number }
  feeds: { total: number; enabled: number; items: FeedItem[] }
  bgp: { connected_peers: number; total_peers: number; peers: Array<{ ip: string; asn: number; name: string; state: string }> }
  modes: Array<unknown>
  user_history: UserHistoryPoint[]
  feed_history: FeedHistoryPoint[]
}

export interface ChartTooltipItem {
  raw: number
}
