export interface UserCIDRMatch {
  category: string
  service: string
  percentage: number
  selected: boolean
}

export interface UserCIDRLookupResult {
  query: string
  matches: UserCIDRMatch[]
  before_percentage: number
  after_percentage: number
  in_tunnel: boolean
}
