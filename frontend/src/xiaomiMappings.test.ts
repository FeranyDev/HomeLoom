import { describe, expect, it } from 'vitest'
import { inferXiaomiDeviceType, requiredXiaomiProperties } from './xiaomiMappings'

describe('Xiaomi temperature/humidity mapping', () => {
	it('infers the combined model before the single-property sensor models', () => {
		expect(inferXiaomiDeviceType({ did: '1', name: '米家温湿度传感器', model: 'temperature-humidity-monitor' })).toBe('temperature-humidity-sensor')
		expect(inferXiaomiDeviceType({ did: '2', name: '温湿度计' })).toBe('temperature-humidity-sensor')
		expect(inferXiaomiDeviceType({ did: '3', name: '温度计' })).toBe('single-property-sensor')
		expect(inferXiaomiDeviceType({ did: '4', name: '湿度计' })).toBe('single-property-sensor')
	})

	it('uses one generic value path for a single-property sensor', () => {
		expect(requiredXiaomiProperties('single-property-sensor')).toEqual([
			expect.objectContaining({ capabilityId: 'sensor', propertyId: 'value' }),
		])
	})

	it('creates both required unified properties', () => {
		expect(requiredXiaomiProperties('temperature-humidity-sensor')).toEqual([
			expect.objectContaining({ capabilityId: 'temperature', propertyId: 'current-temperature' }),
			expect.objectContaining({ capabilityId: 'humidity', propertyId: 'current-humidity' }),
		])
	})
})
