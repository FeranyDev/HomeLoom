import type { Device } from '../types/device'

interface DeviceCardProps {
  device: Device
  pending: boolean
  onPowerChange: (device: Device, value: boolean) => void
  onDetails: (device: Device) => void
}

export function DeviceCard({ device, pending, onPowerChange, onDetails }: DeviceCardProps) {
  const isSwitch = device.type === 'switch'

  return (
    <article className="device-card">
      <div className="device-card__topline">
        <span className={`status-dot ${device.online ? 'is-online' : ''}`} />
        <span>{device.online ? '在线' : '离线'}</span>
        <span className="provider">{device.providerId}</span>
      </div>
      <h2>{device.name}</h2>
      <p className="device-kind">{isSwitch ? '虚拟开关' : '温度传感器'}</p>

      {isSwitch ? (
        <button
          className={`power-button ${device.state.power ? 'is-on' : ''}`}
          disabled={pending || !device.online}
          onClick={() => onPowerChange(device, !device.state.power)}
        >
          <span>{pending ? '同步中' : device.state.power ? '已开启' : '已关闭'}</span>
          <span className="switch-track"><span /></span>
        </button>
      ) : (
        <div className="temperature">
          <strong>{device.state.temperature?.toFixed(1)}</strong>
          <span>°C</span>
        </div>
      )}

      <footer><span>更新于 {new Date(device.lastUpdateAt).toLocaleTimeString('zh-CN')}</span><button onClick={() => onDetails(device)}>查看详情</button></footer>
    </article>
  )
}
