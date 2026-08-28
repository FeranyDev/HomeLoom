import { availabilityLabel, deviceProperty, runtimeModeLabel, type Device } from '../types/device'
import { deviceTypeLabel } from '../presentationLabels'
import { DeviceTypeIcon } from './DeviceTypeIcon'
import { deviceLocationLabel } from '../deviceLocation'

interface DeviceCardProps {
  device: Device
  pending: boolean
	onPowerChange: (device: Device, value: boolean) => void
	onDetails: (device: Device) => void
	onMapping?: (device: Device) => void
	onEnabledChange?: (device: Device, enabled: boolean) => void
}

export function DeviceCard({ device, pending, onPowerChange, onDetails, onMapping, onEnabledChange }: DeviceCardProps) {
  const networkDevice = device.type === 'network-device'
  const hasPower = device.type === 'switch' || device.type === 'lightbulb' || device.type === 'outlet' || networkDevice
  const kind = deviceTypeLabel(device.type)
  const power = deviceProperty(device, 'switch', 'power')?.bool ?? false
	const wakePending = networkDevice && (deviceProperty(device, 'network', 'wake-pending')?.bool ?? false)
	const networkWakeEnabled = (device.endpoints ?? []).some((endpoint) => (endpoint.capabilities ?? []).some((capability) => capability.id === 'switch' && (capability.properties ?? []).some((property) => property.definition.id === 'power' && property.definition.writable)))
  const temperature = deviceProperty(device, 'temperature', 'current-temperature')?.number
  const humidity = deviceProperty(device, 'humidity', 'current-humidity')?.number
	const measurement = device.type === 'temperature-sensor' ? { value: temperature, unit: '°C' }
		: device.type === 'humidity-sensor' ? { value: humidity, unit: '%' }
		: device.type === 'pressure-sensor' ? { value: deviceProperty(device, 'pressure', 'current-pressure')?.number, unit: 'hPa' }
		: device.type === 'noise-sensor' ? { value: deviceProperty(device, 'noise', 'current-level')?.number, unit: 'dB' }
		: device.type === 'water-level-sensor' ? { value: deviceProperty(device, 'water-level', 'current-level')?.number, unit: '%' }
		: device.type === 'soil-moisture-sensor' ? { value: deviceProperty(device, 'soil-moisture', 'current-moisture')?.number, unit: '%' }
		: null
  const contact = deviceProperty(device, 'contact', 'contact-detected')?.bool
  const motion = deviceProperty(device, 'motion', 'motion-detected')?.bool
  const advancedCapability = device.type === 'fan' ? 'fan' : 'air-purifier'
  const active = deviceProperty(device, advancedCapability, 'active')?.bool
  const speed = deviceProperty(device, advancedCapability, 'rotation-speed')?.number
  const filterLife = deviceProperty(device, 'filter', 'life-level')?.number
  const position = deviceProperty(device, 'window-covering', 'current-position')?.int
	const headingID = `device-${device.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`
	const updatedAt = new Date(device.lastUpdateAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })

  return (
	<article className={`device-card is-${device.type}${device.mappingError ? ' has-mapping-error' : ''}`} aria-labelledby={headingID}>
      <div className="device-card__topline">
        <span className={`status-dot is-${device.availability}`} />
		<span>{device.removed ? '来源已删除' : device.disabled ? '已禁用' : networkDevice ? (power ? '已开启' : wakePending ? '启动中' : '已关闭') : availabilityLabel(device.availability)}</span>
		{device.runtimeMode && <span className={`device-runtime-mode is-${device.runtimeMode}`}>{runtimeModeLabel(device.runtimeMode, device.stateTransport)}</span>}
		<span className="provider">{device.providerId}</span>
      </div>
	  <div className="device-card__identity">
		<DeviceTypeIcon type={device.type} />
		<div><h2 id={headingID}>{device.name}</h2><p className="device-kind">{kind}</p></div>
	  </div>

	  <div className="device-card__value">{hasPower ? (
        <button
          className={`power-button ${power ? 'is-on' : ''}`}
		  disabled={pending || (networkDevice ? power || wakePending || !networkWakeEnabled : !device.online)}
          onClick={() => onPowerChange(device, networkDevice ? true : !power)}
        >
		  <span>{pending ? (networkDevice ? '正在发送唤醒包' : '同步中') : networkDevice ? (power ? '已开启' : wakePending ? '正在启动 · 等待探测确认' : networkWakeEnabled ? '已关闭 · 点击唤醒' : '已关闭 · 仅监测') : !device.online ? '不可用' : power ? '已开启' : '已关闭'}</span>
          <span className="switch-track"><span /></span>
        </button>
      ) : measurement ? (
        <div className="temperature">
          <strong>{device.online ? measurement.value?.toFixed(1) : '—'}</strong>
          <span>{measurement.unit}</span>
        </div>
      ) : device.type === 'temperature-humidity-sensor' ? <div className="dual-sensor"><div><strong>{device.online ? temperature?.toFixed(1) : '—'}</strong><span>°C</span></div><div><strong>{device.online ? humidity?.toFixed(1) : '—'}</strong><span>%</span></div></div>
        : device.type === 'fan' || device.type === 'air-purifier' ? <div className={`sensor-state ${device.online && active ? 'is-active' : ''}`}><strong>{!device.online ? '不可用' : `${active ? '运行中' : '已停止'} · ${speed?.toFixed(0) ?? 0}%`}</strong><span>{device.type === 'fan' ? 'FAN' : `AIR · 滤芯 ${filterLife?.toFixed(0) ?? '—'}%`}</span></div>
        : device.type === 'window-covering' ? <div className="temperature"><strong>{device.online ? position ?? '—' : '—'}</strong><span>%</span></div>
        : device.type === 'contact-sensor' || device.type === 'motion-sensor' ? <div className={`sensor-state ${device.online && (contact || motion) ? 'is-active' : ''}`}><strong>{!device.online ? '不可用' : device.type === 'contact-sensor' ? (contact ? '已闭合' : '已打开') : (motion ? '检测到活动' : '无活动')}</strong><span>{device.type === 'contact-sensor' ? 'CONTACT' : 'MOTION'}</span></div>
        : <div className={`sensor-state ${device.online ? 'is-active' : ''}`}><strong>{device.online ? '状态可用' : '不可用'}</strong><span>{String(device.type).toUpperCase()}</span></div>
      }</div>

	  {device.mappingError && <section className="device-card__mapping-error" role="alert"><strong>属性映射异常</strong><p>{device.mappingError}</p></section>}
	  <dl className="device-card__metadata"><div><dt>设备来源</dt><dd>{device.providerId}</dd></div><div><dt>统一模型</dt><dd>{device.type}</dd></div><div><dt>家庭 / 房间</dt><dd>{deviceLocationLabel(device)}{device.locationMode === 'custom' ? ' · HomeLoom 自定义' : ' · 继承来源'}</dd></div><div><dt>上次更新</dt><dd>{updatedAt}</dd></div></dl>
	  <footer><div className="device-card__actions"><button onClick={() => onDetails(device)}>查看详情</button>{onMapping && !device.removed && <button className="is-primary" onClick={() => onMapping(device)}>配置映射</button>}{onEnabledChange && !device.removed && <button className="device-card__disable" disabled={pending} onClick={() => onEnabledChange(device, Boolean(device.disabled))}>{device.disabled ? '重新启用' : '禁用设备'}</button>}</div></footer>
    </article>
  )
}
