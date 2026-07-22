export interface ProviderCapabilities {
  discovery: boolean
  propertyRead: boolean
  propertyWrite: boolean
	commands?: boolean
  events: boolean
}

export interface ProviderManifest { id: string; type: string; name: string; version: string }
export interface ProviderCredentialStatus { managed: boolean; refreshAt?: string; tokenExpiresAt?: string; certificateExpiresAt?: string }

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
	diagnostics?: Record<string, string>
	credentials?: ProviderCredentialStatus
	credentialError?: string
	credentialRetryAt?: string
}

export type ProviderInput = ProviderConfig
