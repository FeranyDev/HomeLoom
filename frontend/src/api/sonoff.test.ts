import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginSonoff } from './sonoff'

describe('Sonoff account API', () => {
	beforeEach(() => {
		vi.restoreAllMocks()
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { accessToken: 'access', region: 'cn', endpoint: 'https://cn-apia.coolkit.cn' } }), { status: 200 })))
	})

	it('submits eWeLink credentials without exposing them in the result', async () => {
		await expect(loginSonoff({ username: 'user@example.com', password: 'secret', countryCode: '+86', region: 'auto' })).resolves.toEqual({ accessToken: 'access', region: 'cn', endpoint: 'https://cn-apia.coolkit.cn' })
		expect(fetch).toHaveBeenCalledWith('/api/v1/sonoff/login', expect.objectContaining({ method: 'POST', body: JSON.stringify({ username: 'user@example.com', password: 'secret', countryCode: '+86', region: 'auto' }) }))
	})
})
