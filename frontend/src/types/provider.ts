export interface ProviderCapabilities {
  discovery: boolean
  propertyRead: boolean
  propertyWrite: boolean
	commands?: boolean
  events: boolean
}

export interface ProviderManifest { id: string; type: string; name: string; version: string }

export interface ProviderConfig {
  id: string
  type: string
  name: string
  enabled: boolean
  config: Record<string, unknown>
}

export interface Provider extends ProviderConfig {
  status: string
  error?: string
  manifest?: ProviderManifest
  capabilities: ProviderCapabilities
  retryCount: number
  nextRetryAt?: string
  transitionedAt?: string
	metrics?: Record<string, number>
}

export type ProviderInput = ProviderConfig
