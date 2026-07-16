import type { XiaomiHubDevice } from './api/xiaomi'

export const xiaomiDeviceTypes = [
	['switch', '开关'], ['lightbulb', '灯'], ['outlet', '插座'], ['single-property-sensor', '单属性传感器'], ['temperature-humidity-sensor', '温湿度传感器'], ['contact-sensor', '门窗传感器'], ['motion-sensor', '人体传感器'], ['fan', '风扇'], ['air-purifier', '空气净化器'], ['window-covering', '窗帘'],
	['illuminance-sensor', '照度传感器'], ['occupancy-sensor', '占用传感器'], ['leak-sensor', '漏水传感器'], ['smoke-sensor', '烟雾传感器'],
	['carbon-monoxide-sensor', '一氧化碳传感器'], ['carbon-dioxide-sensor', '二氧化碳传感器'], ['air-quality-sensor', '空气质量传感器'],
	['thermostat', '恒温器'], ['air-conditioner', '空调'], ['heater-cooler', '冷暖设备'], ['humidifier-dehumidifier', '加湿除湿器'], ['lock', '门锁'], ['garage-door', '车库门'],
	['security-system', '安防系统'], ['valve', '阀门'], ['speaker', '扬声器'], ['robot-vacuum', '扫地机器人'],
] as const

export function inferXiaomiDeviceType(item: XiaomiHubDevice): string {
	const hint = `${item.name} ${item.model ?? ''} ${item.specType ?? ''}`.toLowerCase()
	if (/light|lamp|灯/.test(hint)) return 'lightbulb'
	if (/outlet|plug|插座/.test(hint)) return 'outlet'
	if (/温湿度/.test(hint) || ((/temperature|thermometer|温度/.test(hint)) && (/humidity|湿度/.test(hint)))) return 'temperature-humidity-sensor'
	if (/temperature|thermometer|温度|humidity|湿度/.test(hint)) return 'single-property-sensor'
	if (/garage|车库门/.test(hint)) return 'garage-door'
	if (/smart.?lock|door.?lock|门锁/.test(hint)) return 'lock'
	if (/contact|door|window|门窗/.test(hint)) return 'contact-sensor'
	if (/illuminance|lux|light.?sensor|照度|光照/.test(hint)) return 'illuminance-sensor'
	if (/leak|water.?sensor|漏水|水浸/.test(hint)) return 'leak-sensor'
	if (/smoke|烟雾/.test(hint)) return 'smoke-sensor'
	if (/carbon.?monoxide|\bco\b|一氧化碳/.test(hint)) return 'carbon-monoxide-sensor'
	if (/carbon.?dioxide|\bco2\b|二氧化碳/.test(hint)) return 'carbon-dioxide-sensor'
	if (/air.?quality|空气质量/.test(hint)) return 'air-quality-sensor'
	if (/occupancy|presence|存在|占用/.test(hint)) return 'occupancy-sensor'
	if (/motion|occupancy|人体|移动/.test(hint)) return 'motion-sensor'
	if (/purifier|air-purifier|净化器/.test(hint)) return 'air-purifier'
	if (/curtain|cover|窗帘/.test(hint)) return 'window-covering'
	if (/thermostat|恒温器/.test(hint)) return 'thermostat'
	if (/air.?condition(?:er)?|空调/.test(hint)) return 'air-conditioner'
	if (/heater.?cooler|冷暖风机|冷暖机/.test(hint)) return 'heater-cooler'
	if (/humidifier|dehumidifier|加湿|除湿/.test(hint)) return 'humidifier-dehumidifier'
	if (/security.?system|alarm.?system|安防|报警主机/.test(hint)) return 'security-system'
	if (/valve|阀门|水阀/.test(hint)) return 'valve'
	if (/speaker|音箱|扬声器/.test(hint)) return 'speaker'
	if (/robot.?vacuum|vacuum|扫地|吸尘/.test(hint)) return 'robot-vacuum'
	if (/fan|风扇/.test(hint)) return 'fan'
	return 'switch'
}

export function requiredXiaomiProperties(type: string) {
	const property = (capabilityId: string, propertyId: string, name: string, valueType: string, piid: number, writable: boolean, enumValues?: Record<string, number>) => ({ endpointId: 'main', capabilityId, capabilityType: capabilityId, propertyId, name, valueType, siid: 2, piid, writable, notifiable: true, ...(enumValues ? { enum: enumValues } : {}) })
	switch (type) {
	case 'single-property-sensor': return [property('sensor', 'value', '传感器值', 'number', 1, false)]
	case 'temperature-humidity-sensor': return [property('temperature', 'current-temperature', '当前温度', 'number', 1, false), property('humidity', 'current-humidity', '当前湿度', 'number', 2, false)]
	case 'contact-sensor': return [property('contact', 'contact-detected', '接触状态', 'bool', 1, false)]
	case 'motion-sensor': return [property('motion', 'motion-detected', '活动状态', 'bool', 1, false)]
	case 'fan': return [property('fan', 'active', '启用', 'bool', 1, true), property('fan', 'current-state', '当前状态', 'enum', 2, false, { inactive: 0, idle: 1, active: 2 })]
	case 'air-purifier': return [property('air-purifier', 'active', '启用', 'bool', 1, true), property('air-purifier', 'current-state', '当前状态', 'enum', 2, false, { inactive: 0, idle: 1, active: 2 })]
	case 'window-covering': return [property('window-covering', 'current-position', '当前位置', 'int', 1, false), property('window-covering', 'target-position', '目标位置', 'int', 2, true), property('window-covering', 'position-state', '运动状态', 'enum', 3, false, { decreasing: 0, increasing: 1, stopped: 2 })]
	case 'illuminance-sensor': return [property('illuminance', 'current-illuminance', '当前照度', 'number', 1, false)]
	case 'occupancy-sensor': return [property('occupancy', 'occupancy-detected', '占用状态', 'bool', 1, false)]
	case 'leak-sensor': return [property('leak', 'leak-detected', '漏水状态', 'bool', 1, false)]
	case 'smoke-sensor': return [property('smoke', 'smoke-detected', '烟雾状态', 'bool', 1, false)]
	case 'carbon-monoxide-sensor': return [property('carbon-monoxide', 'detected', '一氧化碳告警', 'bool', 1, false)]
	case 'carbon-dioxide-sensor': return [property('carbon-dioxide', 'detected', '二氧化碳告警', 'bool', 1, false)]
	case 'air-quality-sensor': return [property('air-quality', 'current-air-quality', '当前空气质量', 'enum', 1, false, { unknown: 0, excellent: 1, good: 2, fair: 3, inferior: 4, poor: 5 })]
	case 'thermostat': return [property('thermostat', 'current-state', '当前工作状态', 'enum', 1, false, { off: 0, heating: 1, cooling: 2, idle: 3 }), property('thermostat', 'target-mode', '目标模式', 'enum', 2, true, { off: 0, heat: 1, cool: 2, auto: 3 }), property('temperature', 'current-temperature', '当前温度', 'number', 3, false), property('temperature', 'target-temperature', '目标温度', 'number', 4, true)]
	case 'air-conditioner': return [property('air-conditioner', 'active', '启用', 'bool', 1, true), property('air-conditioner', 'target-mode', '运行模式', 'enum', 2, true, { auto: 0, cool: 1, dry: 2, heat: 3, fan: 4 }), { ...property('temperature', 'target-temperature', '目标温度', 'number', 3, true), unit: 'celsius', min: 16, max: 32, step: 0.5 }]
	case 'heater-cooler': return [property('heater-cooler', 'active', '启用', 'bool', 1, true), property('heater-cooler', 'current-state', '当前工作状态', 'enum', 2, false, { inactive: 0, idle: 1, heating: 2, cooling: 3 }), property('heater-cooler', 'target-state', '目标模式', 'enum', 3, true, { auto: 0, heat: 1, cool: 2 }), property('temperature', 'current-temperature', '当前温度', 'number', 4, false)]
	case 'humidifier-dehumidifier': return [property('humidifier-dehumidifier', 'active', '启用', 'bool', 1, true), property('humidifier-dehumidifier', 'current-state', '当前工作状态', 'enum', 2, false, { inactive: 0, idle: 1, humidifying: 2, dehumidifying: 3 }), property('humidifier-dehumidifier', 'target-state', '目标模式', 'enum', 3, true, { auto: 0, humidify: 1, dehumidify: 2 }), property('humidity', 'current-humidity', '当前湿度', 'number', 4, false), property('humidity', 'target-humidity', '目标湿度', 'number', 5, true)]
	case 'lock': return [property('lock', 'current-state', '当前锁定状态', 'enum', 1, false, { unsecured: 0, secured: 1, jammed: 2, unknown: 3 }), property('lock', 'target-state', '目标锁定状态', 'enum', 2, true, { unsecured: 0, secured: 1 })]
	case 'garage-door': return [property('garage-door', 'current-state', '当前门状态', 'enum', 1, false, { open: 0, closed: 1, opening: 2, closing: 3, stopped: 4, unknown: 5 }), property('garage-door', 'target-state', '目标门状态', 'enum', 2, true, { open: 0, closed: 1 })]
	case 'security-system': return [property('security-system', 'current-state', '当前布防状态', 'enum', 1, false, { 'stay-arm': 0, 'away-arm': 1, 'night-arm': 2, disarmed: 3, triggered: 4 }), property('security-system', 'target-state', '目标布防状态', 'enum', 2, true, { 'stay-arm': 0, 'away-arm': 1, 'night-arm': 2, disarmed: 3 })]
	case 'valve': return [property('valve', 'active', '启用', 'bool', 1, true), property('valve', 'in-use', '正在使用', 'bool', 2, false), property('valve', 'valve-type', '阀门类型', 'enum', 3, false, { generic: 0, irrigation: 1, shower: 2, faucet: 3 })]
	case 'speaker': return [property('speaker', 'active', '启用', 'bool', 1, true), property('speaker', 'volume', '音量', 'number', 2, true), property('speaker', 'mute', '静音', 'bool', 3, true)]
	case 'robot-vacuum': return [property('robot-vacuum', 'active', '启用', 'bool', 1, true), property('robot-vacuum', 'current-state', '当前工作状态', 'enum', 2, false, { idle: 0, cleaning: 1, paused: 2, returning: 3, charging: 4, error: 5 }), property('robot-vacuum', 'target-mode', '目标模式', 'enum', 3, true, { vacuum: 0, mop: 1, 'vacuum-and-mop': 2, spot: 3 })]
	default: return [property('switch', 'power', '开关', 'bool', 1, true)]
	}
}

export function stableXiaomiID(did: string) {
	return `xiaomi-${did.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[-._]+|[-._]+$/g, '') || 'device'}`.slice(0, 63)
}
