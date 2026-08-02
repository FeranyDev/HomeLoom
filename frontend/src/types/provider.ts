interface ProviderCapabilities {
  discovery: boolean
  propertyRead: boolean
  propertyWrite: boolean
	commands?: boolean
  events: boolean
}

interface ProviderManifest { id: string; type: string; name: string; version: string }
interface ProviderCredentialStatus { managed: boolean; refreshAt?: string; tokenExpiresAt?: string; certificateExpiresAt?: string }

/**
 * A short-lived authentication challenge retained by a Provider runtime.
 * Xiaomi's account service has used both `verification_required` and
 * `auth_required` for this state, so consumers should key off the challenge
 * fields rather than one exact status string.
 */
export interface ProviderAuthChallenge {
  status: string
  challengeId?: string
  /** Compatibility aliases accepted from older Xiaomi API shards. */
  challenge_id?: string
  verificationUrl?: string
  verification_url?: string
  expiresAt?: string
  expires_at?: string
  url?: string
  message?: string
}

interface ProviderConfig {
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
	authChallenge?: ProviderAuthChallenge | null
	credentialError?: string
	credentialRetryAt?: string
}

export type ProviderInput = ProviderConfig
