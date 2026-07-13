import type { Device } from '../types/device'

interface DeviceCardProps {
  device: Device
  pending: boolean
  onPowerChange: (device: Device, value: boolean) => void
  onDetails: (device: Device) => void
}

export function DeviceCard({ device, pending, onPowerChange, onDetails }: DeviceCardProps) {
  const hasPower = device.type === 'switch' || device.type === 'lightbulb' || device.type === 'outlet'
  const kind = device.type === 'lightbulb' ? '灯泡' : device.type === 'outlet' ? '插座' : device.type === 'switch' ? '开关' : device.type === 'humidity-sensor' ? '湿度传感器' : device.type === 'contact-sensor' ? '接触传感器' : device.type === 'motion-sensor' ? '活动传感器' : '温度传感器'
  const propertyValue = (capability: string, property: string) => device.endpoints.flatMap((endpoint) => endpoint.capabilities).find((item) => item.id === capability)?.properties.find((item) => item.definition.id === property)?.value
  const humidity = propertyValue('humidity', 'current-humidity')?.number
  const contact = propertyValue('contact', 'contact-detected')?.bool
  const motion = propertyValue('motion', 'motion-detected')?.bool

  return (
    <article className="device-card">
      <div className="device-card__topline">
        <span className={`status-dot ${device.online ? 'is-online' : ''}`} />
        <span>{device.online ? '在线' : '离线'}</span>
        <span className="provider">{device.providerId}</span>
      </div>
      <h2>{device.name}</h2>
      <p className="device-kind">{kind}</p>

      {hasPower ? (
        <button
          className={`power-button ${device.state.power ? 'is-on' : ''}`}
          disabled={pending || !device.online}
          onClick={() => onPowerChange(device, !device.state.power)}
        >
          <span>{pending ? '同步中' : device.state.power ? '已开启' : '已关闭'}</span>
          <span className="switch-track"><span /></span>
        </button>
      ) : device.type === 'temperature-sensor' ? (
        <div className="temperature">
          <strong>{device.state.temperature?.toFixed(1)}</strong>
          <span>°C</span>
        </div>
      ) : device.type === 'humidity-sensor' ? <div className="temperature"><strong>{humidity?.toFixed(1)}</strong><span>%</span></div>
        : <div className={`sensor-state ${(contact || motion) ? 'is-active' : ''}`}><strong>{device.type === 'contact-sensor' ? (contact ? '已闭合' : '已打开') : (motion ? '检测到活动' : '无活动')}</strong><span>{device.type === 'contact-sensor' ? 'CONTACT' : 'MOTION'}</span></div>
      }

      <footer><span>更新于 {new Date(device.lastUpdateAt).toLocaleTimeString('zh-CN')}</span><button onClick={() => onDetails(device)}>查看详情</button></footer>
    </article>
  )
}
