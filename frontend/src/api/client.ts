interface ApiErrorBody { code?: string; message?: string; requestId?: string; fields?: Record<string, string> }

export class ApiError extends Error {
  constructor(message: string, public readonly status: number, public readonly fields: Record<string, string> = {}, public readonly code = 'unknown_error', public readonly requestId = '') { super(message); this.name = 'ApiError' }
}

export interface DownloadedFile {
	blob: Blob
	filename: string
}

function cookieValue(name: string): string {
  if (typeof document === 'undefined') return ''
  const prefix = `${encodeURIComponent(name)}=`
  const item = document.cookie.split(';').map((value) => value.trim()).find((value) => value.startsWith(prefix))
  return item ? decodeURIComponent(item.slice(prefix.length)) : ''
}

async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers)
	const formData = typeof FormData !== 'undefined' && init.body instanceof FormData
  if (init.body && !formData && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
	const method = (init.method ?? 'GET').toUpperCase()
	if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && !headers.has('X-CSRF-Token')) {
		const csrfToken = cookieValue('homeloom_csrf')
		if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
	}
  const response = await fetch(path, { credentials: 'same-origin', ...init, headers })
  if (!response.ok) {
    const body = await response.json().catch(() => null) as ApiErrorBody | null
		if (response.status === 401 && !path.startsWith('/api/v1/auth/') && typeof window !== 'undefined') window.dispatchEvent(new Event('homeloom:unauthorized'))
		if (response.status === 403 && body?.message === 'invalid CSRF token' && typeof window !== 'undefined') window.dispatchEvent(new Event('homeloom:unauthorized'))
    throw new ApiError(body?.message || `请求失败 (${response.status})`, response.status, body?.fields, body?.code, body?.requestId)
  }
	return response
}

// requestStream shares authentication, CSRF, and error handling with JSON
// requests while leaving the response body available to an SSE reader.
export async function requestStream(path: string, init: RequestInit = {}): Promise<Response> {
  return request(path, init)
}

export async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
	const response = await request(path, init)
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export async function requestData<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await requestJSON<{ data: T }>(path, init)
  return response?.data as T
}

export async function requestFile(path: string, init: RequestInit = {}): Promise<DownloadedFile> {
	const response = await request(path, init)
	const disposition = response.headers.get('Content-Disposition') ?? ''
	const encoded = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1]
	const plain = disposition.match(/filename="?([^";]+)"?/i)?.[1]
	return { blob: await response.blob(), filename: decodeURIComponent(encoded ?? plain ?? 'download.bin') }
}
