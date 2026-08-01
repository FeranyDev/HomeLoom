import type { DeviceType, ParameterLevel, ValueType } from './types/device'
import type { MappingTransformType } from './types/mapping'
import type { TargetType } from './types/target'

const deviceTypes: Record<DeviceType, string> = {
	switch: '开关', lightbulb: '灯泡', outlet: '插座',
	'temperature-sensor': '温度传感器', 'humidity-sensor': '湿度传感器',
	'temperature-humidity-sensor': '温湿度传感器',
	'pressure-sensor': '气压传感器', 'noise-sensor': '噪声传感器',
	'water-level-sensor': '水位传感器', 'soil-moisture-sensor': '土壤湿度传感器',
	'contact-sensor': '接触传感器', 'motion-sensor': '活动传感器',
	fan: '风扇', 'air-purifier': '空气净化器', 'window-covering': '窗帘',
	'illuminance-sensor': '照度传感器', 'occupancy-sensor': '占用传感器', 'leak-sensor': '漏水传感器',
	'smoke-sensor': '烟雾传感器', 'carbon-monoxide-sensor': '一氧化碳传感器',
	'carbon-dioxide-sensor': '二氧化碳传感器', 'air-quality-sensor': '空气质量传感器',
	thermostat: '恒温器', 'air-conditioner': '空调', 'heater-cooler': '冷暖设备', 'humidifier-dehumidifier': '加湿除湿器',
	lock: '门锁', 'garage-door': '车库门', 'security-system': '安防系统', valve: '阀门',
	pump: '水泵', 'water-heater': '热水器', 'power-meter': '电力计量器', 'ev-charger': '电动汽车充电桩',
	speaker: '扬声器', television: '电视', 'robot-vacuum': '扫地机器人',
}

const valueTypes: Record<ValueType, string> = { bool: '布尔值', int: '整数', number: '数值', string: '文本', enum: '枚举' }
const parameterLevels: Record<ParameterLevel, string> = { required: '必需', optional: '可选', custom: '自定义' }
const targetTypes: Record<TargetType, string> = { 'apple-hap': 'Apple 家庭桥', 'homekit-camera': 'HomeKit 摄像头', matter: 'Matter 桥', 'matter-camera': 'Matter 摄像头（实验性）' }
const transformTypes: Record<MappingTransformType, string> = {
	invert: '布尔反转', reciprocal: '数值倒数', 'int-number': '整数转数值', scale: '数值缩放', clamp: '范围裁剪', enum: '枚举映射', unit: '单位转换',
	'range-enum': '数值分段转枚举', 'enum-number': '枚举转数值', threshold: '数值阈值转布尔', 'bool-number': '布尔转数值', 'bool-enum': '布尔转枚举', 'enum-bool': '枚举转布尔',
	'map-range': '区间线性映射', round: '数值取整', 'parse-number': '文本解析为数值', 'number-string': '数值格式化为文本',
}
const unitNames: Record<string, string> = {
	percent: '百分比', degree: '角度', mired: '微倒度', watt: '瓦特', 'kilowatt-hour': '千瓦时',
	'microgram-per-cubic-meter': '微克/立方米', celsius: '摄氏度', fahrenheit: '华氏度', kelvin: '开尔文',
	lux: '勒克斯', ppm: '百万分比', second: '秒', hour: '小时', count: '次',
	volt: '伏特', ampere: '安培', hertz: '赫兹', ratio: '比值', hectopascal: '百帕',
	kilopascal: '千帕', decibel: '分贝', liter: '升', 'liter-per-minute': '升/分钟',
	'square-meter': '平方米', millimeter: '毫米', 'gram-per-cubic-meter': '克/立方米',
	'microsiemens-per-centimeter': '微西门子/厘米', aqi: '空气质量指数',
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
	sensor: '传感器', value: '传感器值', illuminance: '照度', 'current-illuminance': '当前照度',
	occupancy: '占用', 'occupancy-detected': '占用状态', leak: '漏水', 'leak-detected': '漏水状态',
	smoke: '烟雾', 'smoke-detected': '烟雾状态', 'carbon-monoxide': '一氧化碳', 'carbon-dioxide': '二氧化碳',
	detected: '告警状态', 'current-level': '当前浓度', 'peak-level': '峰值浓度', 'pm10-density': 'PM10 浓度',
	'carbon-dioxide-level': '二氧化碳浓度', 'nitrogen-dioxide-density': '二氧化氮浓度', 'ozone-density': '臭氧浓度',
	thermostat: '恒温器', 'target-mode': '目标模式', 'target-temperature': '目标温度',
	'heating-threshold': '制热阈值', 'cooling-threshold': '制冷阈值', 'display-units': '显示温标',
	'air-conditioner': '空调', 'vertical-swing': '上下扫风', 'horizontal-swing': '左右扫风',
	'wind-direction': '导风方向', 'auxiliary-heat': '辅热', 'sleep-mode': '睡眠模式',
	'heater-cooler': '冷暖设备', 'humidifier-dehumidifier': '加湿除湿器', 'target-humidity': '目标湿度',
	'water-level': '水位', lock: '门锁', jammed: '锁舌卡住', 'garage-door': '车库门',
	'security-system': '安防系统', 'alarm-type': '告警类型', valve: '阀门', 'valve-type': '阀门类型',
	'set-duration': '设定时长', 'remaining-duration': '剩余时长', speaker: '扬声器', volume: '音量',
	mute: '静音', 'current-media-state': '当前媒体状态', 'target-media-state': '目标媒体状态', 'input-source': '输入源', television: '电视', 'remote-key': '遥控按键',
	'robot-vacuum': '扫地机器人', 'cleaning-progress': '清洁进度', fault: '故障代码', charging: '正在充电',
	camera: '摄像头', privacy: '隐私模式', indicator: '状态指示灯', 'night-vision': '夜视模式',
	'motion-detection': '移动侦测', media: '媒体',
	'live-stream': '实时视频', snapshot: '快照', microphone: '麦克风', talkback: '双向语音',
	ptz: '云台控制', movement: '移动方向', 'movement-speed': '移动速度',
	'pan-position': '水平位置', 'tilt-position': '垂直位置', 'zoom-level': '变焦倍率',
	'current-preset': '当前记忆点', 'target-preset': '目标记忆点', 'preset-count': '记忆点数量',
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

const matterClusterNames: Record<string, string> = {
	OnOff: '开关', LevelControl: '亮度控制', ColorControl: '颜色控制', TemperatureMeasurement: '温度测量',
	RelativeHumidityMeasurement: '相对湿度测量', BooleanState: '布尔状态', OccupancySensing: '占用感知',
	WindowCovering: '窗帘控制', FanControl: '风扇控制', Thermostat: '恒温器', DoorLock: '门锁',
	MediaPlayback: '媒体播放', KeypadInput: '遥控输入',
	BridgedDeviceBasicInformation: '桥接设备基本信息', Descriptor: '设备描述',
}

const matterMemberNames: Record<string, string> = {
	OnOff: '开关状态', CurrentLevel: '当前亮度', MoveToLevel: '设置亮度', CurrentHue: '当前色相', CurrentSaturation: '当前饱和度',
	CurrentTemperature: '当前温度', MeasuredValue: '测量值', Occupancy: '占用状态', PresentValue: '当前值',
	CurrentPositionLiftPercent100ths: '当前位置', TargetPositionLiftPercent100ths: '目标位置', PercentSetting: '百分比设定',
	FanMode: '风扇模式', PercentCurrent: '当前转速', OccupiedCoolingSetpoint: '制冷设定温度', OccupiedHeatingSetpoint: '制热设定温度',
	LockState: '门锁状态', LockDoor: '上锁', UnlockDoor: '解锁', Reachable: '可达状态',
	CurrentState: '当前播放状态', Play: '播放', Pause: '暂停', Stop: '停止', SendKey: '发送遥控按键',
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

export function matterClusterLabel(cluster: string): string { return bilingual(cluster, matterClusterNames[cluster]) }

function matterMemberLabel(member: string): string { return bilingual(member, matterMemberNames[member]) }

export function matterConsumerPathLabel(cluster: string, member: string, kind: 'attribute' | 'command'): string {
	const memberKind = kind === 'command' ? '命令' : '属性'
	return `${matterClusterLabel(cluster)} → ${memberKind}：${matterMemberLabel(member)}`
}

export function permissionLabel(readable: boolean, writable: boolean, notifiable: boolean): string {
	const raw = `${readable ? 'R' : '–'}${writable ? 'W' : '–'}${notifiable ? 'N' : '–'}`
	const translated = [readable && '读', writable && '写', notifiable && '通知'].filter(Boolean).join(' / ') || '无权限'
	return `${translated}（${raw}）`
}
