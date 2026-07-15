import { availabilityLabel, deviceProperty, type Device } from '../types/device'
import { deviceTypeLabel } from '../presentationLabels'

interface DeviceCardProps {
  device: Device
  pending: boolean
  onPowerChange: (device: Device, value: boolean) => void
  onDetails: (device: Device) => void
	onMapping?: (device: Device) => void
	onEnabledChange?: (device: Device, enabled: boolean) => void
}

export function DeviceCard({ device, pending, onPowerChange, onDetails, onMapping, onEnabledChange }: DeviceCardProps) {
  const hasPower = device.type === 'switch' || device.type === 'lightbulb' || device.type === 'outlet'
  const kind = deviceTypeLabel(device.type)
  const power = deviceProperty(device, 'switch', 'power')?.bool ?? false
  const temperature = deviceProperty(device, 'temperature', 'current-temperature')?.number
  const humidity = deviceProperty(device, 'humidity', 'current-humidity')?.number
	const singleSensor = device.endpoints.flatMap((endpoint) => endpoint.capabilities).find((capability) => capability.id === 'sensor')?.properties.find((property) => property.definition.id === 'value')
	const sensorValue = singleSensor?.value.number
	const sensorUnit = singleSensor?.definition.unit === 'celsius' ? '°C' : singleSensor?.definition.unit === 'percent' ? '%' : singleSensor?.definition.unit ?? ''
  const contact = deviceProperty(device, 'contact', 'contact-detected')?.bool
  const motion = deviceProperty(device, 'motion', 'motion-detected')?.bool
  const advancedCapability = device.type === 'fan' ? 'fan' : 'air-purifier'
  const active = deviceProperty(device, advancedCapability, 'active')?.bool
  const speed = deviceProperty(device, advancedCapability, 'rotation-speed')?.number
  const filterLife = deviceProperty(device, 'filter', 'life-level')?.number
  const position = deviceProperty(device, 'window-covering', 'current-position')?.int

  return (
    <article className="device-card">
      <div className="device-card__topline">
        <span className={`status-dot is-${device.availability}`} />
        <span>{device.removed ? '来源已删除' : device.disabled ? '已禁用' : availabilityLabel(device.availability)}</span>
        <span className="provider">{device.providerId}</span>
      </div>
      <h2>{device.name}</h2>
      <p className="device-kind">{kind}</p>

      {hasPower ? (
        <button
          className={`power-button ${power ? 'is-on' : ''}`}
          disabled={pending || !device.online}
          onClick={() => onPowerChange(device, !power)}
        >
          <span>{pending ? '同步中' : !device.online ? '不可用' : power ? '已开启' : '已关闭'}</span>
          <span className="switch-track"><span /></span>
        </button>
      ) : device.type === 'single-property-sensor' ? (
        <div className="temperature">
          <strong>{device.online ? sensorValue?.toFixed(1) : '—'}</strong>
          <span>{sensorUnit}</span>
        </div>
      ) : device.type === 'temperature-humidity-sensor' ? <div className="dual-sensor"><div><strong>{device.online ? temperature?.toFixed(1) : '—'}</strong><span>°C</span></div><div><strong>{device.online ? humidity?.toFixed(1) : '—'}</strong><span>%</span></div></div>
        : device.type === 'fan' || device.type === 'air-purifier' ? <div className={`sensor-state ${device.online && active ? 'is-active' : ''}`}><strong>{!device.online ? '不可用' : `${active ? '运行中' : '已停止'} · ${speed?.toFixed(0) ?? 0}%`}</strong><span>{device.type === 'fan' ? 'FAN' : `AIR · 滤芯 ${filterLife?.toFixed(0) ?? '—'}%`}</span></div>
        : device.type === 'window-covering' ? <div className="temperature"><strong>{device.online ? position ?? '—' : '—'}</strong><span>%</span></div>
        : <div className={`sensor-state ${device.online && (contact || motion) ? 'is-active' : ''}`}><strong>{!device.online ? '不可用' : device.type === 'contact-sensor' ? (contact ? '已闭合' : '已打开') : (motion ? '检测到活动' : '无活动')}</strong><span>{device.type === 'contact-sensor' ? 'CONTACT' : 'MOTION'}</span></div>
      }

      <footer><span>更新于 {new Date(device.lastUpdateAt).toLocaleTimeString('zh-CN')}</span><div><button onClick={() => onDetails(device)}>查看详情</button>{onMapping && !device.removed && <button onClick={() => onMapping(device)}>配置映射</button>}{onEnabledChange && !device.removed && <button disabled={pending} onClick={() => onEnabledChange(device, Boolean(device.disabled))}>{device.disabled ? '重新启用' : '禁用设备'}</button>}</div></footer>
    </article>
  )
}
