import type { Provider, ProviderInput } from '../types/provider'
import { requestData, requestJSON } from './client'

export async function listProviders(signal?: AbortSignal): Promise<Provider[]> {
  return requestData<Provider[]>('/api/v1/providers', { signal })
}

export async function saveProvider(input: ProviderInput, editing: boolean): Promise<Provider> {
  const path = editing ? `/api/v1/providers/${encodeURIComponent(input.id)}` : '/api/v1/providers'
  return requestData<Provider>(path, { method: editing ? 'PUT' : 'POST', body: JSON.stringify(input) })
}

export async function deleteProvider(id: string): Promise<void> {
  await requestJSON<void>(`/api/v1/providers/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function restartProvider(id: string): Promise<Provider> {
	return requestData<Provider>(`/api/v1/providers/${encodeURIComponent(id)}/restart`, { method: 'POST' })
}

export interface ProviderCredentialRevocation {
	providerId: string
	localRevoked: boolean
	remoteAttempted: boolean
	remoteRevoked: boolean
	remoteError?: string
	disconnectError?: string
	reconciliationError?: string
}

export async function revokeProviderCredentials(id: string, confirmation: string): Promise<ProviderCredentialRevocation> {
	return requestData<ProviderCredentialRevocation>(`/api/v1/providers/${encodeURIComponent(id)}/credentials/revoke`, {
		method: 'POST', body: JSON.stringify({ confirmation }),
	})
}

export async function testProviderConnection(input: ProviderInput): Promise<void> {
  await requestData<{ reachable: boolean }>('/api/v1/providers/test', { method: 'POST', body: JSON.stringify(input) })
}

export interface ProviderDiscoveryCandidate {
  id?: string
  providerType: string
  name: string
  host: string
  port: number
  mac: string
  metadata?: Record<string, string>
}

export async function scanProviderNetwork(input: ProviderInput): Promise<ProviderDiscoveryCandidate[]> {
  return requestData<ProviderDiscoveryCandidate[]>('/api/v1/providers/scan', { method: 'POST', body: JSON.stringify(input) })
}
