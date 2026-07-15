import type { DeviceType, ParameterLevel, ValueType } from './types/device'
import type { MappingTransformType } from './types/mapping'
import type { TargetType } from './types/target'

const deviceTypes: Record<DeviceType, string> = {
	switch: '开关', lightbulb: '灯泡', outlet: '插座',
	'single-property-sensor': '单属性传感器',
	'temperature-humidity-sensor': '温湿度传感器',
	'contact-sensor': '接触传感器', 'motion-sensor': '活动传感器',
	fan: '风扇', 'air-purifier': '空气净化器', 'window-covering': '窗帘',
}

const valueTypes: Record<ValueType, string> = { bool: '布尔值', int: '整数', number: '数值', string: '文本', enum: '枚举' }
const parameterLevels: Record<ParameterLevel, string> = { required: '必需', optional: '可选', custom: '自定义' }
const targetTypes: Record<TargetType, string> = { 'apple-hap': 'Apple 家庭桥', matter: 'Matter 桥' }
const transformTypes: Record<MappingTransformType, string> = { invert: '布尔反转', scale: '数值缩放', clamp: '范围裁剪', enum: '枚举映射', unit: '单位转换' }
const unitNames: Record<string, string> = {
	percent: '百分比', degree: '角度', mired: '微倒度', watt: '瓦特', 'kilowatt-hour': '千瓦时',
	'microgram-per-cubic-meter': '微克/立方米', celsius: '摄氏度', fahrenheit: '华氏度',
}

const resourceNames: Record<string, string> = {
	main: '主端点', switch: '开关', power: '开关状态', light: '灯光', brightness: '亮度',
	'color-temperature': '色温', hue: '色相', saturation: '饱和度', outlet: '插座', 'in-use': '使用状态',
	temperature: '温度', 'current-temperature': '当前温度', humidity: '湿度', 'current-humidity': '当前湿度',
	contact: '接触', 'contact-detected': '接触状态', motion: '活动', 'motion-detected': '活动状态',
	fan: '风扇', active: '启用状态', 'current-state': '当前状态', 'target-state': '目标状态',
	'rotation-speed': '转速', 'swing-mode': '摆风模式', 'rotation-direction': '旋转方向',
	'lock-physical-controls': '物理控制锁', 'air-purifier': '空气净化器', 'air-quality': '空气质量',
	'current-air-quality': '当前空气质量', 'pm2.5-density': 'PM2.5 浓度', 'voc-density': 'VOC 浓度',
	filter: '滤芯', 'life-level': '剩余寿命', 'change-indication': '更换提示', 'window-covering': '窗帘',
	'current-position': '当前位置', 'target-position': '目标位置', 'position-state': '运动状态',
	'obstruction-detected': '障碍物检测', battery: '电池', level: '电量', low: '低电量', security: '安全状态', tampered: '防拆状态',
	sensor: '传感器', value: '传感器值',
}

const serviceNames: Record<string, string> = {
	Switch: '开关服务', Lightbulb: '灯泡服务', Outlet: '插座服务', TemperatureSensor: '温度传感器服务',
	HumiditySensor: '湿度传感器服务', ContactSensor: '接触传感器服务', MotionSensor: '活动传感器服务',
	BatteryService: '电池服务', PrimaryService: '主服务', FanV2: '风扇服务', AirPurifier: '空气净化器服务',
	AirQualitySensor: '空气质量服务', FilterMaintenance: '滤芯维护服务', WindowCovering: '窗帘服务',
}

const characteristicNames: Record<string, string> = {
	On: '开/关', Brightness: '亮度', ColorTemperature: '色温', Hue: '色相', Saturation: '饱和度',
	OutletInUse: '使用状态', CurrentTemperature: '当前温度', CurrentRelativeHumidity: '当前湿度',
	ContactSensorState: '接触状态', MotionDetected: '活动状态', BatteryLevel: '电量', StatusLowBattery: '低电量',
	StatusTampered: '防拆状态', Active: '启用状态', CurrentFanState: '当前风扇状态', TargetFanState: '目标风扇状态',
	RotationSpeed: '转速', SwingMode: '摆风模式', RotationDirection: '旋转方向', LockPhysicalControls: '物理控制锁',
	CurrentAirPurifierState: '当前净化状态', TargetAirPurifierState: '目标净化状态', AirQuality: '空气质量',
	'PM2.5Density': 'PM2.5 浓度', VOCDensity: 'VOC 浓度', FilterLifeLevel: '滤芯寿命',
	FilterChangeIndication: '滤芯更换提示', CurrentPosition: '当前位置', TargetPosition: '目标位置',
	PositionState: '运动状态', ObstructionDetected: '障碍物检测',
}

export function bilingual(raw: string, translated?: string): string {
	return translated && translated !== raw ? `${translated}（${raw}）` : raw
}

export function deviceTypeLabel(type: DeviceType): string { return bilingual(type, deviceTypes[type]) }
export function valueTypeLabel(type: ValueType): string { return bilingual(type, valueTypes[type]) }
export function parameterLevelLabel(level: ParameterLevel): string { return bilingual(level, parameterLevels[level]) }
export function targetTypeLabel(type: TargetType): string { return bilingual(type, targetTypes[type]) }
export function transformTypeLabel(type: MappingTransformType): string { return bilingual(type, transformTypes[type]) }
export function profileKindLabel(kind: 'provider' | 'capability' | 'target'): string {
	return bilingual(kind, { provider: '提供端', capability: '通用能力', target: '目标端' }[kind])
}
export function resourceLabel(id: string): string { return bilingual(id, resourceNames[id]) }
export function unitLabel(unit: string): string { return bilingual(unit, unitNames[unit]) }

export function propertyDisplayLabel(name: string, id: string): string {
	const raw = name === id ? id : `${name} · ${id}`
	return bilingual(raw, resourceNames[id])
}

export function consumerPropertyLabel(id: string): string {
	const separator = id.indexOf('.')
	if (separator < 0) return id
	const service = id.slice(0, separator)
	const characteristic = id.slice(separator + 1)
	const translated = [serviceNames[service], characteristicNames[characteristic]].filter(Boolean).join(' · ')
	return bilingual(id, translated || undefined)
}

export function permissionLabel(readable: boolean, writable: boolean, notifiable: boolean): string {
	const raw = `${readable ? 'R' : '–'}${writable ? 'W' : '–'}${notifiable ? 'N' : '–'}`
	const translated = [readable && '读', writable && '写', notifiable && '通知'].filter(Boolean).join(' / ') || '无权限'
	return `${translated}（${raw}）`
}
