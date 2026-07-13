import { useState } from 'react'
import type { Device } from '../types/device'
import type { Provider } from '../types/provider'

type SimulationValues = { online?: boolean; power?: boolean; temperature?: number }

export function ProviderCard({ provider, devices, onEdit, onDelete, onSimulate }: { provider: Provider; devices: Device[]; onEdit: (provider: Provider) => void; onDelete: (provider: Provider) => void; onSimulate: (device: Device, values: SimulationValues) => Promise<void> }) {
  const features = Object.entries(provider.capabilities || {}).filter(([, enabled]) => enabled).map(([name]) => name)
  const [temperatures, setTemperatures] = useState<Record<string, string>>({})
  return <article className="provider-card">
    <div className="device-card__topline"><span className={`status-dot ${provider.status === 'running' ? 'is-online' : ''}`} />{provider.status}<span className="provider">{provider.type}</span></div>
    <p className="target-id">{provider.id}</p><h2>{provider.name}</h2>
    <div className="capability-list">{features.length ? features.map((item) => <span key={item}>{item}</span>) : <span>未运行</span>}</div>
    {provider.type === 'virtual' && devices.length > 0 && <div className="simulation-panel"><div><strong>运行时模拟</strong><small>状态只保存在内存中</small></div>{devices.map((item) => <div className="simulation-device" key={item.id}><div className="simulation-name"><span className={`status-dot ${item.online ? 'is-online' : ''}`} /><b>{item.name}</b><small>{item.id}</small></div><div className="simulation-actions"><button onClick={() => void onSimulate(item, { online: !item.online })}>{item.online ? '设为离线' : '恢复在线'}</button>{item.type === 'switch' && <button onClick={() => void onSimulate(item, { power: !item.state.power })}>{item.state.power ? '关闭' : '打开'}</button>}{item.type === 'temperature-sensor' && <><input aria-label={`${item.name}温度`} type="number" step="0.1" value={temperatures[item.id] ?? String(item.state.temperature ?? 0)} onChange={(event) => setTemperatures((current) => ({ ...current, [item.id]: event.target.value }))} /><button onClick={() => void onSimulate(item, { temperature: Number(temperatures[item.id] ?? item.state.temperature) })}>上报</button></>}</div></div>)}</div>}
    {provider.error && <p className="inline-error">{provider.error}</p>}
    <div className="target-actions"><button onClick={() => onEdit(provider)}>编辑</button><button className="is-danger" onClick={() => onDelete(provider)}>删除</button></div>
  </article>
}
