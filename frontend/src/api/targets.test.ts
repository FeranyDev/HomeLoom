import { afterEach, describe, expect, it, vi } from 'vitest'
import { closeMatterCommissioningWindow, confirmMatterEndpointDeviceType, deleteMatterFabric, factoryResetMatterTarget, normalizeTarget, openMatterCommissioningWindow, saveTarget } from './targets'

afterEach(() => vi.unstubAllGlobals())

function response(data: unknown) { return new Response(JSON.stringify({ data }), { status: 200, headers: { 'Content-Type': 'application/json' } }) }

describe('Target API boundary', () => {
	it('normalizes legacy flat HomeKit responses into the HAP branch only', () => {
		const target = normalizeTarget({ id: 'apple-main', type: 'apple-hap', name: 'Home', enabled: true, status: 'running', address: ':51826', setupId: 'HLM1', paired: false, pairingCode: '001-02-003', deviceIds: [], devices: [] })
		expect(target).toMatchObject({ type: 'apple-hap', config: { address: ':51826', setupId: 'HLM1' }, pairing: { paired: false, pairingCode: '001-02-003' } })
	})

	it('keeps an independent HomeKit Camera target in its own branch', () => {
		const target = normalizeTarget({
			id: 'camera-homekit-1', type: 'homekit-camera', name: '客厅摄像头', enabled: true, status: 'running',
			address: ':52431', paired: false, pairingCode: '123-45-678',
			deviceIds: ['xiaomi-camera-1'], devices: [{ id: 'xiaomi-camera-1', name: '客厅摄像头', type: 'camera', sourceDeviceId: 'xiaomi-camera-1', enabled: true }],
		})
		expect(target).toMatchObject({
			type: 'homekit-camera', config: { address: ':52431' },
			pairing: { paired: false, pairingCode: '123-45-678' },
		})
	})

	it('normalizes Matter without retaining legacy HomeKit fields', () => {
		const target = normalizeTarget({ id: 'matter-main', type: 'matter', name: 'Matter', enabled: true, status: 'running', address: ':51826', setupId: 'HAP1', paired: true, matterConfig: { networkInterface: 'en0', udpPort: 5540, commissioningWindowSeconds: 900 }, commissioningState: 'window-open', commissioningWindowOpen: true, fabricCount: 1, endpointCount: 2, deviceIds: [], devices: [] })
		expect(target).toMatchObject({ type: 'matter', config: { networkInterface: 'en0', udpPort: 5540, commissioningWindowSeconds: 900 }, commissioning: { state: 'window-open', windowOpen: true }, fabricCount: 1, endpointCount: 2 })
		if (target.type !== 'matter') throw new Error('expected Matter')
		expect('address' in target.config).toBe(false)
		expect('paired' in target).toBe(false)
	})

	it('normalizes and serializes Matter Camera through the Matter-only boundary', async () => {
		const normalized = normalizeTarget({
			id: 'camera-matter-1', type: 'matter-camera', name: 'Matter Camera', enabled: true, status: 'running',
			matterConfig: { udpPort: 5541 }, commissioningState: 'uncommissioned', commissioningWindowOpen: false,
			fabricCount: 0, endpointCount: 1, deviceIds: ['front-camera'],
			devices: [{ id: 'front-camera', name: '门口', type: 'camera', sourceDeviceId: 'front-camera', enabled: true }],
			pairingCode: 'should-not-be-homekit',
		})
		expect(normalized).toMatchObject({ type: 'matter-camera', config: { udpPort: 5541 }, commissioning: { state: 'uncommissioned', windowOpen: false } })
		expect(normalized).not.toHaveProperty('pairing')

		const fetchMock = vi.fn().mockResolvedValue(response(normalized))
		vi.stubGlobal('fetch', fetchMock)
		await saveTarget({
			id: 'camera-matter-1', type: 'matter-camera', name: 'Matter Camera', enabled: true,
			deviceIds: ['front-camera'],
			devices: [{ id: 'front-camera', name: '门口', type: 'camera', sourceDeviceId: 'front-camera', enabled: true }],
			config: { networkInterface: '', udpPort: null, discriminator: null, passcode: null, vendorId: null, productId: null, productName: '', serialNumber: '', commissioningWindowSeconds: null },
		}, false)
		const body = JSON.parse(fetchMock.mock.calls[0][1].body as string)
		expect(body).toMatchObject({ type: 'matter-camera', matterConfig: { passcode: null }, deviceIds: ['front-camera'] })
		expect(body).not.toHaveProperty('pin')
		expect(body).not.toHaveProperty('setupId')
		expect(body).not.toHaveProperty('homeKitConfig')
	})

	it('sends Matter config as its own discriminated payload with explicit automatic nulls', async () => {
		const fetchMock = vi.fn().mockResolvedValue(response({ id: 'matter-main', type: 'matter', name: 'Matter', enabled: true, status: 'starting', config: {}, commissioning: { state: 'unknown', windowOpen: false }, fabricCount: 0, endpointCount: 0, deviceIds: [], devices: [] }))
		vi.stubGlobal('fetch', fetchMock)
		await saveTarget({ id: 'matter-main', type: 'matter', name: 'Matter', enabled: true, deviceIds: [], devices: [], config: { networkInterface: '', udpPort: null, discriminator: null, passcode: null, vendorId: null, productId: null, productName: '', serialNumber: '', commissioningWindowSeconds: null } }, false)
		const body = JSON.parse(fetchMock.mock.calls[0][1].body as string)
		expect(body).toMatchObject({ type: 'matter', matterConfig: { udpPort: null, discriminator: null, passcode: null, vendorId: null, productId: null, commissioningWindowSeconds: null } })
		expect(body).not.toHaveProperty('config')
		expect(body).not.toHaveProperty('address')
		expect(body).not.toHaveProperty('pin')
		expect(body).not.toHaveProperty('setupId')
	})

	it('normalizes bridge issues and diagnostics', () => {
		const target = normalizeTarget({
			id: 'apple-main', type: 'apple-hap', name: 'Home', enabled: true, status: 'running',
			deviceIds: [], devices: [], error: '1 台设备未能发布到桥',
			diagnostics: { skippedAccessories: '1', publishedAccessories: '2' },
			issues: [{ deviceId: 'broken-switch', deviceName: '坏掉的开关', deviceType: 'switch', stage: 'consumer-contract', message: 'requires parameter main/switch/power' }],
		})
		expect(target.error).toContain('未能发布到桥')
		expect(target.diagnostics).toEqual({ skippedAccessories: '1', publishedAccessories: '2' })
		expect(target.issues).toEqual([{ deviceId: 'broken-switch', deviceName: '坏掉的开关', deviceType: 'switch', stage: 'consumer-contract', message: 'requires parameter main/switch/power' }])
	})

	it('keeps legacy HAP fields confined to the compatibility serializer', async () => {
		const fetchMock = vi.fn().mockResolvedValue(response({ id: 'apple-main', type: 'apple-hap', name: 'Home', enabled: true, status: 'running', deviceIds: [], devices: [] }))
		vi.stubGlobal('fetch', fetchMock)
		await saveTarget({ id: 'apple-main', type: 'apple-hap', name: 'Home', enabled: true, deviceIds: [], devices: [], config: { address: ':51826', pin: '00102003', setupId: 'HLM1' } }, true)
		const body = JSON.parse(fetchMock.mock.calls[0][1].body as string)
		expect(body).toMatchObject({ config: { address: ':51826', pin: '00102003', setupId: 'HLM1' }, address: ':51826', pin: '00102003', setupId: 'HLM1' })
	})

	it('calls Matter lifecycle endpoints with narrowly scoped payloads', async () => {
		const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(response({ id: 'matter-main', type: 'matter', name: 'Matter', enabled: true, status: 'running', config: {}, commissioning: { state: 'unknown', windowOpen: false }, fabricCount: 0, endpointCount: 0, deviceIds: [], devices: [] })))
		vi.stubGlobal('fetch', fetchMock)
		await openMatterCommissioningWindow('matter-main', 600, 'OPEN COMMISSIONING matter-main')
		await closeMatterCommissioningWindow('matter-main')
		await deleteMatterFabric('matter-main', 'fabric-1', 'DELETE FABRIC matter-main fabric-1')
		await factoryResetMatterTarget('matter-main', 'FACTORY RESET matter-main')
		await confirmMatterEndpointDeviceType('matter-main', 'lamp-1', 'lightbulb', 'CHANGE ENDPOINT TYPE matter-main lamp-1 lightbulb')
		expect(fetchMock.mock.calls.map((call) => [call[0], (call[1] as RequestInit).method, (call[1] as RequestInit).body])).toEqual([
			['/api/v1/targets/matter-main/commissioning-window', 'POST', JSON.stringify({ durationSeconds: 600, confirmation: 'OPEN COMMISSIONING matter-main' })],
			['/api/v1/targets/matter-main/commissioning-window', 'DELETE', undefined],
			['/api/v1/targets/matter-main/fabrics/fabric-1', 'DELETE', JSON.stringify({ confirmation: 'DELETE FABRIC matter-main fabric-1' })],
			['/api/v1/targets/matter-main/factory-reset', 'POST', JSON.stringify({ confirmation: 'FACTORY RESET matter-main' })],
			['/api/v1/targets/matter-main/endpoints/lamp-1/device-type', 'POST', JSON.stringify({ deviceType: 'lightbulb', confirmation: 'CHANGE ENDPOINT TYPE matter-main lamp-1 lightbulb' })],
		])
	})
})
