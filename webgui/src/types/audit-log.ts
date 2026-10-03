export interface AuditLogEntry {
  id: number
  recorded_at: string // ISO (RFC3339)
  actor: string
  user_agent: string
  action: string
  object_type: string
  object_id: string
  before: string
  after: string
}

export interface AuditLogListResponse {
  entries: AuditLogEntry[]
  total: number
}
