export interface User {
  id: number
  name: string
  peer_ip: string
  peer_asn: number
  next_hop: string
  has_password: boolean
  selection_locked: boolean
  enabled: boolean
  filter_override: boolean
  filter_mode: string
  filter_editable: boolean
  catalog_mode_id: number
  catalog_mode_name: string
  catalog_editable: boolean
  active_dial: boolean
  web_auth: string
  networks: string[]
  peer_state: string
  filter_allow: string[]
  filter_deny: string[]
}

export interface Credential {
  login: string
}

export interface UsersListResponse {
  users: User[]
}

export interface CredentialsResponse {
  credentials: Credential[]
}

export interface UserStatusesResponse {
  peer_states: Record<string, string>
}

export interface UserSavePayload {
  name: string
  peer_ip: string
  peer_asn: number
  next_hop: string
  bgp_password: string
  password_enabled: boolean
  selection_locked: boolean
  enabled: boolean
  filter_override: boolean
  filter_mode: string
  filter_editable: boolean
  catalog_mode_id: number
  catalog_editable: boolean
  active_dial: boolean
  web_auth: string
  networks: string[]
  filter_allow: string[]
  filter_deny: string[]
}
