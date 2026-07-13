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
