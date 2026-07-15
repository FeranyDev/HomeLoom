import { describe, expect, it } from 'vitest'
import { consumerPropertyLabel, deviceTypeLabel, permissionLabel, propertyDisplayLabel, unitLabel } from './presentationLabels'

describe('presentation labels', () => {
	it('keeps raw identifiers beside Chinese translations', () => {
		expect(deviceTypeLabel('lightbulb')).toBe('灯泡（lightbulb）')
		expect(consumerPropertyLabel('Lightbulb.On')).toBe('灯泡服务 · 开/关（Lightbulb.On）')
		expect(propertyDisplayLabel('Power', 'power')).toBe('开关状态（Power · power）')
		expect(permissionLabel(true, false, true)).toBe('读 / 通知（R–N）')
		expect(unitLabel('kilowatt-hour')).toBe('千瓦时（kilowatt-hour）')
	})
})
