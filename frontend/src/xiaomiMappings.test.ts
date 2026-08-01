import { describe, expect, it } from 'vitest'
import { defaultXiaomiMedia, inferXiaomiDeviceType, requiredXiaomiProperties, stableXiaomiControlID, stableXiaomiID, xiaomiDeviceTypes } from './xiaomiMappings'

describe('Xiaomi temperature/humidity mapping', () => {
	it('offers every built-in model exactly once', () => {
		const types = xiaomiDeviceTypes.map(([type]) => type)
		expect(types).toHaveLength(37)
		expect(new Set(types).size).toBe(37)
		expect(types).not.toContain('single-property-sensor')
	})

	it('infers cameras and creates a secret-free native media baseline', () => {
		expect(inferXiaomiDeviceType({
			did: 'camera-1',
			name: '客厅摄像头',
			model: 'isa.camera.hlc7',
			specType: 'urn:miot-spec-v2:device:camera:0000A01C',
		})).toBe('camera')
		expect(requiredXiaomiProperties('camera')).toEqual([])
		expect(defaultXiaomiMedia('camera')).toEqual({
			protocol: 'xiaomi-miss',
			subtype: 'hd',
			channel: 1,
			profiles: [expect.objectContaining({
				schemaVersion: 1,
				id: 'main',
				width: 1920,
				height: 1080,
				videoCodec: 'h264',
				audioCodec: 'aac',
			})],
		})
		expect(JSON.stringify(defaultXiaomiMedia('camera'))).not.toMatch(/token|password|user/i)
		expect(stableXiaomiID('1178028045')).toBe('xiaomi-1178028045')
		expect(stableXiaomiControlID('1178028045')).toBe('xiaomi-control-1178028045')
	})

	it('infers the combined model before explicit single-measurement models', () => {
		expect(inferXiaomiDeviceType({ did: '1', name: '米家温湿度传感器', model: 'temperature-humidity-monitor' })).toBe('temperature-humidity-sensor')
		expect(inferXiaomiDeviceType({ did: '2', name: '温湿度计' })).toBe('temperature-humidity-sensor')
		expect(inferXiaomiDeviceType({ did: '3', name: '温度计' })).toBe('temperature-sensor')
		expect(inferXiaomiDeviceType({ did: '4', name: '湿度计' })).toBe('humidity-sensor')
	})

	it('uses semantic value paths for single-measurement sensors', () => {
		expect(requiredXiaomiProperties('temperature-sensor')).toEqual([
			expect.objectContaining({ capabilityId: 'temperature', propertyId: 'current-temperature' }),
		])
		expect(requiredXiaomiProperties('humidity-sensor')).toEqual([
			expect.objectContaining({ capabilityId: 'humidity', propertyId: 'current-humidity' }),
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
		expect(inferXiaomiDeviceType({ did: '10', name: '车库交流充电桩' })).toBe('ev-charger')
		expect(inferXiaomiDeviceType({ did: '11', name: '花园土壤湿度计' })).toBe('soil-moisture-sensor')
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
			expect.objectContaining({ capabilityId: 'air-conditioner', propertyId: 'target-mode', piid: 2, writable: true }),
			expect.objectContaining({ capabilityId: 'temperature', propertyId: 'target-temperature', piid: 3, writable: true, min: 16, max: 32 }),
		])
	})
})
