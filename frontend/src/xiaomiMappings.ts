import type { XiaomiHubDevice } from './api/xiaomi'

export const xiaomiDeviceTypes = [
	['switch', '开关'], ['lightbulb', '灯'], ['outlet', '插座'], ['temperature-sensor', '温度传感器'], ['humidity-sensor', '湿度传感器'], ['contact-sensor', '门窗传感器'], ['motion-sensor', '人体传感器'], ['fan', '风扇'], ['air-purifier', '空气净化器'], ['window-covering', '窗帘'],
] as const

export function inferXiaomiDeviceType(item: XiaomiHubDevice): string {
	const hint = `${item.name} ${item.model ?? ''} ${item.specType ?? ''}`.toLowerCase()
	if (/light|lamp|灯/.test(hint)) return 'lightbulb'
	if (/outlet|plug|插座/.test(hint)) return 'outlet'
	if (/temperature|thermometer|温度/.test(hint)) return 'temperature-sensor'
	if (/humidity|湿度/.test(hint)) return 'humidity-sensor'
	if (/contact|door|window|门窗/.test(hint)) return 'contact-sensor'
	if (/motion|occupancy|人体|移动/.test(hint)) return 'motion-sensor'
	if (/purifier|air-purifier|净化器/.test(hint)) return 'air-purifier'
	if (/curtain|cover|窗帘/.test(hint)) return 'window-covering'
	if (/fan|风扇/.test(hint)) return 'fan'
	return 'switch'
}

export function requiredXiaomiProperties(type: string) {
	const property = (capabilityId: string, propertyId: string, name: string, valueType: string, piid: number, writable: boolean, enumValues?: Record<string, number>) => ({ endpointId: 'main', capabilityId, capabilityType: capabilityId, propertyId, name, valueType, siid: 2, piid, writable, notifiable: true, ...(enumValues ? { enum: enumValues } : {}) })
	switch (type) {
	case 'temperature-sensor': return [property('temperature', 'current-temperature', '当前温度', 'number', 1, false)]
	case 'humidity-sensor': return [property('humidity', 'current-humidity', '当前湿度', 'number', 1, false)]
	case 'contact-sensor': return [property('contact', 'contact-detected', '接触状态', 'bool', 1, false)]
	case 'motion-sensor': return [property('motion', 'motion-detected', '活动状态', 'bool', 1, false)]
	case 'fan': return [property('fan', 'active', '启用', 'bool', 1, true), property('fan', 'current-state', '当前状态', 'enum', 2, false, { inactive: 0, idle: 1, active: 2 })]
	case 'air-purifier': return [property('air-purifier', 'active', '启用', 'bool', 1, true), property('air-purifier', 'current-state', '当前状态', 'enum', 2, false, { inactive: 0, idle: 1, active: 2 })]
	case 'window-covering': return [property('window-covering', 'current-position', '当前位置', 'int', 1, false), property('window-covering', 'target-position', '目标位置', 'int', 2, true), property('window-covering', 'position-state', '运动状态', 'enum', 3, false, { decreasing: 0, increasing: 1, stopped: 2 })]
	default: return [property('switch', 'power', '开关', 'bool', 1, true)]
	}
}

export function stableXiaomiID(did: string) {
	return `xiaomi-${did.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[-._]+|[-._]+$/g, '') || 'device'}`.slice(0, 63)
}
