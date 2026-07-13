import { useState } from 'react'
import { availabilityLabel, deviceProperty, type Device, type DeviceAvailability } from '../types/device'
import type { Provider } from '../types/provider'

type SimulationValues = { availability?: DeviceAvailability; online?: boolean; power?: boolean; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; sequence?: number; repeat?: number }

function propertyBool(device: Device, capability: string, property: string): boolean {
	return deviceProperty(device, capability, property)?.bool ?? false
}

function propertyNumber(device: Device, capability: string, property: string): number {
	return deviceProperty(device, capability, property)?.number ?? 0
}

export function ProviderCard({ provider, devices, onEdit, onDelete, onRestart, onSimulate }: { provider: Provider; devices: Device[]; onEdit: (provider: Provider) => void; onDelete: (provider: Provider) => void; onRestart: (provider: Provider) => Promise<void>; onSimulate: (device: Device, values: SimulationValues) => Promise<void> }) {
  const features = Object.entries(provider.capabilities || {}).filter(([, enabled]) => enabled).map(([name]) => name)
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [restarting, setRestarting] = useState(false)
  const restart = async () => { setRestarting(true); try { await onRestart(provider) } finally { setRestarting(false) } }
  const numericControl = (item: Device, field: 'temperature' | 'humidity', value: number) => <><input aria-label={`${item.name}${field === 'temperature' ? '温度' : '湿度'}`} type="number" step="0.1" value={drafts[item.id] ?? String(value)} onChange={(event) => setDrafts((current) => ({ ...current, [item.id]: event.target.value }))} /><button onClick={() => { const next = Number(drafts[item.id] ?? value); void onSimulate(item, field === 'temperature' ? { temperature: next } : { humidity: next }) }}>上报</button></>
  return <article className="provider-card">
    <div className="device-card__topline"><span className={`status-dot ${provider.status === 'running' ? 'is-online' : ''}`} />{provider.status}<span className="provider">{provider.type}</span></div>
    <p className="target-id">{provider.id}</p><h2>{provider.name}</h2>
    <div className="capability-list">{features.length ? features.map((item) => <span key={item}>{item}</span>) : <span>未运行</span>}</div>
	{provider.type === 'mqtt' && <div className="provider-runtime"><span>消息 {provider.metrics?.messagesReceived ?? 0}</span><span>无效 {provider.metrics?.messagesInvalid ?? 0}</span><span>丢弃 {provider.metrics?.messagesDropped ?? 0}</span><span>命令 {provider.metrics?.commandsPublished ?? 0}</span></div>}
    {provider.type === 'virtual' && devices.length > 0 && <div className="simulation-panel"><div><strong>运行时模拟</strong><small>状态只保存在内存中</small></div>{devices.map((item) => {
			const powered = item.type === 'switch' || item.type === 'lightbulb' || item.type === 'outlet'
			const power = propertyBool(item, 'switch', 'power'); const contact = propertyBool(item, 'contact', 'contact-detected'); const motion = propertyBool(item, 'motion', 'motion-detected')
			return <div className="simulation-device" key={item.id}><div className="simulation-name"><span className={`status-dot is-${item.availability}`} /><b>{item.name}</b><small>{item.id} · {availabilityLabel(item.availability)} · seq {item.sequence ?? '—'}</small></div><div className="simulation-actions"><button onClick={() => void onSimulate(item, { online: !item.online })}>{item.online ? '设为离线' : '恢复在线'}</button><button onClick={() => void onSimulate(item, { availability: 'unknown' })}>设为未知</button><button onClick={() => void onSimulate(item, { repeat: 2 })}>重复事件</button><button onClick={() => void onSimulate(item, { sequence: Math.max(1, (item.sequence ?? 1) - 1) })}>旧序列事件</button>{powered && <button onClick={() => void onSimulate(item, { power: !power })}>{power ? '关闭' : '打开'}</button>}{item.type === 'temperature-sensor' && numericControl(item, 'temperature', propertyNumber(item, 'temperature', 'current-temperature'))}{item.type === 'humidity-sensor' && numericControl(item, 'humidity', propertyNumber(item, 'humidity', 'current-humidity'))}{item.type === 'contact-sensor' && <button onClick={() => void onSimulate(item, { contact: !contact })}>{contact ? '设为打开' : '设为闭合'}</button>}{item.type === 'motion-sensor' && <button onClick={() => void onSimulate(item, { motion: !motion })}>{motion ? '清除活动' : '触发活动'}</button>}</div></div>
    })}</div>}
    {provider.error && <div className="provider-error"><p className="inline-error">{provider.error}</p><small>已自动重试 {provider.retryCount ?? 0} 次{provider.nextRetryAt ? ` · 下次 ${new Date(provider.nextRetryAt).toLocaleTimeString()}` : ''}</small></div>}
    <div className="target-actions"><button onClick={() => onEdit(provider)}>编辑</button>{provider.enabled && <button disabled={restarting} onClick={() => void restart()}>{restarting ? '启动中…' : '重新启动'}</button>}<button className="is-danger" onClick={() => onDelete(provider)}>删除</button></div>
  </article>
}
