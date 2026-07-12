import type { Target, TargetInput } from '../types/target'

interface ApiResponse<T> {
  data: T
}

export async function saveTarget(input: TargetInput, editing: boolean): Promise<Target> {
	const path = editing ? `/api/v1/targets/${encodeURIComponent(input.id)}` : '/api/v1/targets'
	const response = await fetch(path, {
		method: editing ? 'PUT' : 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(input),
	})
	if (!response.ok) {
		const body = await response.json().catch(() => null) as { message?: string } | null
		throw new Error(body?.message || `保存桥失败 (${response.status})`)
	}
	return ((await response.json()) as ApiResponse<Target>).data
}

export async function deleteTarget(id: string): Promise<void> {
	const response = await fetch(`/api/v1/targets/${encodeURIComponent(id)}`, { method: 'DELETE' })
	if (!response.ok) throw new Error(`删除桥失败 (${response.status})`)
}

export async function listTargets(signal?: AbortSignal): Promise<Target[]> {
  const response = await fetch('/api/v1/targets', { signal })
  if (!response.ok) {
    throw new Error(`桥接配置请求失败 (${response.status})`)
  }
  return ((await response.json()) as ApiResponse<Target[]>).data
}

export function pairingQRUrl(id: string): string {
  return `/api/v1/targets/${encodeURIComponent(id)}/pairing-qr`
}
