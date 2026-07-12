import type { Provider, ProviderInput } from '../types/provider'

interface ApiResponse<T> { data: T }

export async function listProviders(signal?: AbortSignal): Promise<Provider[]> {
  const response = await fetch('/api/v1/providers', { signal })
  if (!response.ok) throw new Error(`Provider 配置请求失败 (${response.status})`)
  return ((await response.json()) as ApiResponse<Provider[]>).data
}

export async function saveProvider(input: ProviderInput, editing: boolean): Promise<Provider> {
  const path = editing ? `/api/v1/providers/${encodeURIComponent(input.id)}` : '/api/v1/providers'
  const response = await fetch(path, { method: editing ? 'PUT' : 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) })
  if (!response.ok) { const body = await response.json().catch(() => null) as { message?: string } | null; throw new Error(body?.message || `保存 Provider 失败 (${response.status})`) }
  return ((await response.json()) as ApiResponse<Provider>).data
}

export async function deleteProvider(id: string): Promise<void> {
  const response = await fetch(`/api/v1/providers/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!response.ok) { const body = await response.json().catch(() => null) as { message?: string } | null; throw new Error(body?.message || `删除 Provider 失败 (${response.status})`) }
}
