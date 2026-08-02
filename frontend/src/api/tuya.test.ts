import { beforeEach, describe, expect, it, vi } from 'vitest'
import { completeTuyaOAuth, parseTuyaOAuthCallback, pollTuyaSharingLogin, startTuyaOAuth, startTuyaSharingLogin, tuyaOAuthQRCodeURL, tuyaSharingQRCodeURL } from './tuya'

describe('Tuya OAuth API', () => {
	beforeEach(() => {
		vi.restoreAllMocks()
		vi.stubGlobal('fetch', vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { authorizationUrl: 'https://auth.tuya.example/authorize?state=state-1', state: 'state-1', expiresAt: '2030-01-01T00:00:00Z' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { accessToken: 'access', refreshToken: 'refresh', uid: 'uid-1', expiresAt: '2030-01-01T01:00:00Z' } }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
	})

	it('starts and polls Home Assistant compatible QR login', async () => {
		vi.stubGlobal('fetch', vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { state: 'sharing-state', qrData: 'tuyaSmart--qrLogin?token=qr-token', expiresAt: '2030-01-01T00:05:00Z' } }), { status: 200 }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { status: 'complete', accessToken: 'access', refreshToken: 'refresh', uid: 'uid-1', endpoint: 'https://openapi.tuyaus.com', terminalId: 'terminal-1', expiresAt: '2030-01-01T01:00:00Z' } }), { status: 200 })))
		await expect(startTuyaSharingLogin('user-code')).resolves.toMatchObject({ state: 'sharing-state' })
		await expect(pollTuyaSharingLogin('sharing-state')).resolves.toMatchObject({ status: 'complete', uid: 'uid-1' })
		expect(fetch).toHaveBeenNthCalledWith(1, '/api/v1/tuya/login/start', expect.objectContaining({ method: 'POST', body: JSON.stringify({ userCode: 'user-code' }) }))
		expect(fetch).toHaveBeenNthCalledWith(2, '/api/v1/tuya/login/poll', expect.objectContaining({ method: 'POST', body: JSON.stringify({ state: 'sharing-state' }) }))
		expect(tuyaSharingQRCodeURL('sharing/state')).toBe('/api/v1/tuya/login/qr?state=sharing%2Fstate')
	})

	it('starts and completes authorization-code login', async () => {
		await expect(startTuyaOAuth({ accessId: 'id', accessSecret: 'secret', region: 'cn', authorizationUrl: 'https://auth.tuya.example/authorize', redirectUrl: 'http://localhost/tuya/oauth/callback' })).resolves.toMatchObject({ state: 'state-1' })
		await expect(completeTuyaOAuth({ state: 'state-1', code: 'code-1' })).resolves.toMatchObject({ uid: 'uid-1', refreshToken: 'refresh' })
		expect(fetch).toHaveBeenNthCalledWith(1, '/api/v1/tuya/oauth/start', expect.objectContaining({ method: 'POST' }))
		expect(fetch).toHaveBeenNthCalledWith(2, '/api/v1/tuya/oauth/complete', expect.objectContaining({ method: 'POST', body: JSON.stringify({ state: 'state-1', code: 'code-1' }) }))
		expect(tuyaOAuthQRCodeURL('state/1')).toBe('/api/v1/tuya/oauth/qr?state=state%2F1')
	})

	it('accepts only HomeLoom Tuya callback messages', () => {
		expect(parseTuyaOAuthCallback({ type: 'other', code: 'code' })).toBeNull()
		expect(parseTuyaOAuthCallback({ type: 'homeloom-tuya-oauth', code: 'code', state: 'state', error: '' })).toEqual({ type: 'homeloom-tuya-oauth', code: 'code', state: 'state', error: '' })
		expect(parseTuyaOAuthCallback(null)).toBeNull()
	})
})
