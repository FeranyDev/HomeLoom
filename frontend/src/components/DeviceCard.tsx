import { availabilityLabel, deviceProperty, type Device } from '../types/device'

interface DeviceCardProps {
  device: Device
  pending: boolean
  onPowerChange: (device: Device, value: boolean) => void
  onDetails: (device: Device) => void
	onEnabledChange?: (device: Device, enabled: boolean) => void
}

export function DeviceCard({ device, pending, onPowerChange, onDetails, onEnabledChange }: DeviceCardProps) {
  const hasPower = device.type === 'switch' || device.type === 'lightbulb' || device.type === 'outlet'
  const kind = device.type === 'lightbulb' ? '灯泡' : device.type === 'outlet' ? '插座' : device.type === 'switch' ? '开关' : device.type === 'humidity-sensor' ? '湿度传感器' : device.type === 'contact-sensor' ? '接触传感器' : device.type === 'motion-sensor' ? '活动传感器' : '温度传感器'
  const power = deviceProperty(device, 'switch', 'power')?.bool ?? false
  const temperature = deviceProperty(device, 'temperature', 'current-temperature')?.number
  const humidity = deviceProperty(device, 'humidity', 'current-humidity')?.number
  const contact = deviceProperty(device, 'contact', 'contact-detected')?.bool
  const motion = deviceProperty(device, 'motion', 'motion-detected')?.bool

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
      ) : device.type === 'temperature-sensor' ? (
        <div className="temperature">
          <strong>{device.online ? temperature?.toFixed(1) : '—'}</strong>
          <span>°C</span>
        </div>
      ) : device.type === 'humidity-sensor' ? <div className="temperature"><strong>{device.online ? humidity?.toFixed(1) : '—'}</strong><span>%</span></div>
        : <div className={`sensor-state ${device.online && (contact || motion) ? 'is-active' : ''}`}><strong>{!device.online ? '不可用' : device.type === 'contact-sensor' ? (contact ? '已闭合' : '已打开') : (motion ? '检测到活动' : '无活动')}</strong><span>{device.type === 'contact-sensor' ? 'CONTACT' : 'MOTION'}</span></div>
      }

      <footer><span>更新于 {new Date(device.lastUpdateAt).toLocaleTimeString('zh-CN')}</span><div><button onClick={() => onDetails(device)}>查看详情</button>{onEnabledChange && !device.removed && <button disabled={pending} onClick={() => onEnabledChange(device, Boolean(device.disabled))}>{device.disabled ? '重新启用' : '禁用设备'}</button>}</div></footer>
    </article>
  )
}
