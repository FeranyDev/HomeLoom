import { afterEach, describe, expect, it, vi } from 'vitest'
import { scanProviderNetwork } from './providers'

afterEach(() => vi.unstubAllGlobals())

describe('Provider discovery API', () => {
	it('scans a transient provider configuration without changing the provider path', async () => {
		const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: [{ providerType: 'gree', name: '客厅空调', host: '192.168.1.42', port: 7000, mac: 'aabbccddeeff' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
		vi.stubGlobal('fetch', fetchMock)
		await expect(scanProviderNetwork({ id: '', name: '', type: 'gree', enabled: false, config: { devices: [] } })).resolves.toEqual([expect.objectContaining({ host: '192.168.1.42', mac: 'aabbccddeeff' })])
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/providers/scan', expect.objectContaining({ method: 'POST', body: JSON.stringify({ id: '', name: '', type: 'gree', enabled: false, config: { devices: [] } }) }))
	})
})
