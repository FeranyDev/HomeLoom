import { describe, expect, it } from 'vitest'
import { consumerPropertyLabel, deviceTypeLabel, permissionLabel, propertyDisplayLabel, unitLabel } from './presentationLabels'

describe('presentation labels', () => {
	it('keeps raw identifiers beside Chinese translations', () => {
		expect(deviceTypeLabel('lightbulb')).toBe('灯泡（lightbulb）')
		expect(deviceTypeLabel('temperature-humidity-sensor')).toBe('温湿度传感器（temperature-humidity-sensor）')
		expect(deviceTypeLabel('temperature-sensor')).toBe('温度传感器（temperature-sensor）')
		expect(deviceTypeLabel('ev-charger')).toBe('电动汽车充电桩（ev-charger）')
		expect(deviceTypeLabel('robot-vacuum')).toBe('扫地机器人（robot-vacuum）')
		expect(deviceTypeLabel('air-conditioner')).toBe('空调（air-conditioner）')
		expect(deviceTypeLabel('network-device')).toBe('网络设备（network-device）')
		expect(unitLabel('ppm')).toBe('百万分比（ppm）')
		expect(consumerPropertyLabel('Lightbulb.On')).toBe('灯泡服务 · 开/关（Lightbulb.On）')
		expect(propertyDisplayLabel('Power', 'power')).toBe('开关状态（Power · power）')
		expect(propertyDisplayLabel('reachable', 'reachable')).toBe('在线状态（reachable）')
		expect(permissionLabel(true, false, true)).toBe('读 / 通知（R–N）')
		expect(unitLabel('kilowatt-hour')).toBe('千瓦时（kilowatt-hour）')
	})
})
