import { describe, expect, it } from 'vitest'
import type { Device } from './device'
import { deviceProperty, normalizeDevice } from './device'

describe('deviceProperty', () => {
	it('treats null endpoint collections from property-less camera payloads as empty', () => {
		const camera = {
			schemaVersion: 1,
			id: 'camera-1',
			providerId: 'xiaomi-main',
			name: 'Camera',
			type: 'camera',
			availability: 'online',
			online: true,
			endpoints: null,
			lastUpdateAt: new Date(0).toISOString(),
		} as unknown as Device

		expect(deviceProperty(camera, 'switch', 'power')).toBeUndefined()
	})
})

describe('normalizeDevice', () => {
	it('normalizes missing and null endpoint collections', () => {
		expect(normalizeDevice({ id: 'missing' }).endpoints).toEqual([])
		expect(normalizeDevice({ id: 'null', endpoints: null }).endpoints).toEqual([])
	})

	it('normalizes nested provider collections and drops malformed properties', () => {
		const device = normalizeDevice({
			id: 'sonoff-1',
			endpoints: [{ id: 'main', capabilities: [{ id: 'switch', properties: null }, { id: 'sensor', properties: [null, { value: 1 }, { definition: { id: 'temperature' }, value: 22 }] }] }],
		})
		expect(device.endpoints[0].capabilities[0].properties).toEqual([])
		expect(device.endpoints[0].capabilities[1].properties).toEqual([{ definition: { id: 'temperature' }, value: 22 }])
	})
})
