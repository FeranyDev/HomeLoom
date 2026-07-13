export interface ApiErrorBody { code?: string; message?: string; requestId?: string; fields?: Record<string, string> }

export class ApiError extends Error {
  constructor(message: string, public readonly status: number, public readonly fields: Record<string, string> = {}, public readonly code = 'unknown_error', public readonly requestId = '') { super(message); this.name = 'ApiError' }
}

export async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...init, headers })
  if (!response.ok) {
    const body = await response.json().catch(() => null) as ApiErrorBody | null
    throw new ApiError(body?.message || `请求失败 (${response.status})`, response.status, body?.fields, body?.code, body?.requestId)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export async function requestData<T>(path: string, init: RequestInit = {}): Promise<T> {
  return (await requestJSON<{ data: T }>(path, init)).data
}
