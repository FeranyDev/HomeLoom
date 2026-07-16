import { afterEach, describe, expect, it, vi } from 'vitest'
import { completeXiaomiOAuth, discoverXiaomiDevices, discoverXiaomiGateways, startXiaomiCloudLogin, startXiaomiOAuth, verifyXiaomiCloudLogin } from './xiaomi'

afterEach(() => vi.unstubAllGlobals())

describe('Xiaomi API', () => {
	it('starts and completes OAuth and discovers gateways', async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { authorizationUrl: 'https://account.xiaomi.com/oauth2/authorize', state: 'state', oauthUuid: 'uuid', virtualDid: '123' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { oauth: { accessToken: 'secret' }, clientId: '123', caCertificate: 'ca', clientCertificate: 'cert', privateKey: 'key' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ instance: 'hub', hostName: 'hub.local', addresses: ['192.168.1.50'], port: 8883, role: 1, mqttEnabled: true }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ did: '1', name: 'Light', model: 'vendor.light.v1' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ did: '2', name: 'Wi-Fi AC', model: 'xiaomi.aircondition.v1', homeId: 'home-main', homeName: '我的家', roomId: 'room-living', roomName: '客厅', localIp: '192.168.1.20', localAvailable: true }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { status: 'verification_required', challengeId: 'challenge-1', verificationUrl: 'https://account.xiaomi.com/verify' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
			.mockResolvedValueOnce(new Response(JSON.stringify({ data: { status: 'verified', userId: '42', ssecurity: 'security', serviceToken: 'token' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
		vi.stubGlobal('fetch', fetchMock)

		await expect(startXiaomiOAuth({ clientId: '1', region: 'cn', redirectUrl: 'http://homeassistant.local:8123' })).resolves.toMatchObject({ virtualDid: '123' })
		await expect(completeXiaomiOAuth({ clientId: '1', region: 'cn', redirectUrl: 'http://homeassistant.local:8123', code: 'code', state: 'state' })).resolves.toMatchObject({ clientId: '123', privateKey: 'key' })
		await expect(discoverXiaomiGateways()).resolves.toEqual([expect.objectContaining({ instance: 'hub', mqttEnabled: true })])
		await expect(discoverXiaomiDevices('xiaomi-main')).resolves.toEqual([expect.objectContaining({ did: '1', name: 'Light' })])
		await expect(discoverXiaomiDevices('xiaomi-miot-cloud-main', 'xiaomi-miot-cloud')).resolves.toEqual([expect.objectContaining({ did: '2', name: 'Wi-Fi AC', homeName: '我的家', roomName: '客厅', localAvailable: true })])
		await expect(startXiaomiCloudLogin({ region: 'cn', username: 'owner', password: 'password' })).resolves.toMatchObject({ status: 'verification_required', challengeId: 'challenge-1' })
		await expect(verifyXiaomiCloudLogin({ challengeId: 'challenge-1', code: '123456' })).resolves.toMatchObject({ status: 'verified', userId: '42' })
		expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/v1/xiaomi/oauth/start', expect.objectContaining({ method: 'POST' }))
		expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/v1/xiaomi/gateways', expect.anything())
		expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/v1/xiaomi/providers/xiaomi-main/devices', expect.anything())
		expect(fetchMock).toHaveBeenNthCalledWith(5, '/api/v1/xiaomi-miot-cloud/providers/xiaomi-miot-cloud-main/devices', expect.anything())
		expect(fetchMock).toHaveBeenNthCalledWith(6, '/api/v1/xiaomi-miot-cloud/login/start', expect.objectContaining({ method: 'POST' }))
		expect(fetchMock).toHaveBeenNthCalledWith(7, '/api/v1/xiaomi-miot-cloud/login/verify', expect.objectContaining({ method: 'POST' }))
	})
})
