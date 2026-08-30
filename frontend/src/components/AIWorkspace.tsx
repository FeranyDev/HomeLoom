import { useMemo, useState } from 'react'
import type { Device } from '../types/device'
import { deviceTypeLabel } from '../presentationLabels'
import { AIServiceSettings } from './AIServiceSettings'
import { AIInteractionWorkspace } from './AIInteractionWorkspace'
import { DeviceMCPSettings } from './DeviceMCPSettings'
import { HelpTooltip } from './HelpTooltip'

export function AIWorkspace({ devices }: { devices: Device[] }) {
  const [query, setQuery] = useState('')
  const visibleDevices = useMemo(() => {
    const keyword = query.trim().toLocaleLowerCase()
    if (!keyword) return devices
    return devices.filter((device) => `${device.name} ${device.id} ${device.providerId} ${device.homeName ?? ''} ${device.roomName ?? ''}`.toLocaleLowerCase().includes(keyword))
  }, [devices, query])

  return <section className="ai-workspace" aria-label="AI 服务与设备授权">
    <AIServiceSettings />
    <AIInteractionWorkspace devices={devices} />
    <section className="ai-authorization" aria-labelledby="ai-authorization-heading">
      <div className="ai-authorization-heading">
        <div><span>AI 授权</span><h3 id="ai-authorization-heading"><HelpTooltip label="AI 授权说明" content="默认不开放。AI 只能读取已授权的状态；设备操作仍需按权限确认。">设备与属性</HelpTooltip></h3></div>
      </div>
      <label className="ai-device-search">筛选设备<input aria-label="筛选 AI 授权设备" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="名称、ID、来源、家庭或房间" /></label>
      <p className="ai-device-count" role="status">{visibleDevices.length} / {devices.length} 台设备</p>
      <div className="ai-device-authorizations">
        {visibleDevices.map((device) => <article key={device.id}>
          <header><div><strong>{device.name}</strong><small>{deviceTypeLabel(device.type)} · {device.providerId}</small></div><code>{device.id}</code></header>
          <DeviceMCPSettings device={device} />
        </article>)}
        {visibleDevices.length === 0 && <p className="ai-authorization-empty">没有匹配的设备。</p>}
      </div>
    </section>
  </section>
}
