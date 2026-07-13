import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, requestData, requestJSON } from './client'

afterEach(() => vi.unstubAllGlobals())

describe('API client', () => {
  it('unwraps data responses', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { id: 'one' } }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    await expect(requestData<{ id: string }>('/test')).resolves.toEqual({ id: 'one' })
  })

  it('preserves status and field errors', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ code: 'bad_request', message: 'invalid config', requestId: 'request-123', fields: { name: 'required' } }), { status: 400, headers: { 'Content-Type': 'application/json' } })))
    const error = await requestData('/test').catch((cause) => cause)
    expect(error).toBeInstanceOf(ApiError); if (!(error instanceof ApiError)) throw error
		expect(error.status).toBe(400); expect(error.message).toBe('invalid config'); expect(error.fields).toEqual({ name: 'required' }); expect(error.code).toBe('bad_request'); expect(error.requestId).toBe('request-123')
  })

	it('handles empty success responses and malformed error bodies', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 })).mockResolvedValueOnce(new Response('gateway unavailable', { status: 502 })))
		await expect(requestJSON<void>('/empty')).resolves.toBeUndefined()
		const error = await requestJSON('/broken').catch((cause) => cause)
		expect(error).toBeInstanceOf(ApiError)
		expect(error).toMatchObject({ status: 502, code: 'unknown_error', requestId: '' })
		expect((error as Error).message).toBe('请求失败 (502)')
	})

	it('adds same-origin credentials and the CSRF cookie to mutations', async () => {
		document.cookie = 'homeloom_csrf=csrf-token; Path=/'
		const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
		vi.stubGlobal('fetch', fetchMock)
		await requestJSON<void>('/api/v1/test', { method: 'POST' })
		const init = fetchMock.mock.calls[0][1] as RequestInit
		expect(init.credentials).toBe('same-origin')
		expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-token')
		document.cookie = 'homeloom_csrf=; Max-Age=0; Path=/'
	})
})
