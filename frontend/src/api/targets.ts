import type { Target, TargetInput } from '../types/target'
import { requestData, requestJSON } from './client'

export async function saveTarget(input: TargetInput, editing: boolean): Promise<Target> {
	const path = editing ? `/api/v1/targets/${encodeURIComponent(input.id)}` : '/api/v1/targets'
	return requestData<Target>(path, {
		method: editing ? 'PUT' : 'POST',
		body: JSON.stringify(input),
	})
}

export async function deleteTarget(id: string): Promise<void> {
	await requestJSON<void>(`/api/v1/targets/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function regenerateTargetPairing(id: string, confirmation: string): Promise<Target> {
	return requestData<Target>(`/api/v1/targets/${encodeURIComponent(id)}/pairing/regenerate`, { method: 'POST', body: JSON.stringify({ confirmation }) })
}

export function clearTargetPairingIdentity(id: string, confirmation: string): Promise<Target> {
	return requestData<Target>(`/api/v1/targets/${encodeURIComponent(id)}/pairing-identity`, { method: 'DELETE', body: JSON.stringify({ confirmation }) })
}

export async function listTargets(signal?: AbortSignal): Promise<Target[]> {
  return requestData<Target[]>('/api/v1/targets', { signal })
}

export function pairingQRUrl(id: string): string {
  return `/api/v1/targets/${encodeURIComponent(id)}/pairing-qr`
}

export function subscribeTargets(onTarget: (target: Target) => void): () => void {
  const source = new EventSource('/api/v1/events/targets')
  source.addEventListener('target', (event) => { try { onTarget(JSON.parse(event.data) as Target) } catch { /* ignore malformed events */ } })
  return () => source.close()
}
