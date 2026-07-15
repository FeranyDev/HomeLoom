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

	it('infers expanded environmental, security and appliance models', () => {
		expect(inferXiaomiDeviceType({ did: '5', name: '米家水浸卫士' })).toBe('leak-sensor')
		expect(inferXiaomiDeviceType({ did: '6', name: '智能门锁 Pro' })).toBe('lock')
		expect(inferXiaomiDeviceType({ did: '7', name: '扫地机器人' })).toBe('robot-vacuum')
		expect(inferXiaomiDeviceType({ did: '8', name: '二氧化碳监测器' })).toBe('carbon-dioxide-sensor')
		expect(inferXiaomiDeviceType({ did: '9', name: '米家空调伴侣 Pro' })).toBe('air-conditioner')
	})

	it('creates every required path for an expanded model', () => {
		expect(requiredXiaomiProperties('thermostat')).toEqual([
			expect.objectContaining({ capabilityId: 'thermostat', propertyId: 'current-state', writable: false }),
			expect.objectContaining({ capabilityId: 'thermostat', propertyId: 'target-mode', writable: true }),
			expect.objectContaining({ capabilityId: 'temperature', propertyId: 'current-temperature', writable: false }),
			expect.objectContaining({ capabilityId: 'temperature', propertyId: 'target-temperature', writable: true }),
		])
	})

	it('creates the complete required air-conditioner mapping baseline', () => {
		expect(requiredXiaomiProperties('air-conditioner')).toEqual([
			expect.objectContaining({ capabilityId: 'air-conditioner', propertyId: 'active', writable: true }),
			expect.objectContaining({ capabilityId: 'air-conditioner', propertyId: 'current-state', writable: false }),
			expect.objectContaining({ capabilityId: 'air-conditioner', propertyId: 'target-mode', writable: true }),
			expect.objectContaining({ capabilityId: 'temperature', propertyId: 'current-temperature', writable: false }),
			expect.objectContaining({ capabilityId: 'temperature', propertyId: 'target-temperature', writable: true }),
		])
	})
})
