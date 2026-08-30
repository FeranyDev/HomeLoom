import { useEffect, useMemo, useState } from 'react'
import { deleteLogicalDevice, getLogicalDeviceExplanations, listLogicalDeviceCandidates, listLogicalDevices, saveLogicalDevice } from '../api/logicalDevices'
import type { Device } from '../types/device'
import type { LogicalDeviceCandidate, LogicalDeviceConfig, LogicalRouteExplanation } from '../types/logicalDevice'
import { HelpTooltip } from './HelpTooltip'

type LogicalDeviceAPI = {
  list: typeof listLogicalDevices; candidates: typeof listLogicalDeviceCandidates; save: typeof saveLogicalDevice; remove: typeof deleteLogicalDevice; explanations: typeof getLogicalDeviceExplanations
}
const defaultAPI: LogicalDeviceAPI = { list: listLogicalDevices, candidates: listLogicalDeviceCandidates, save: saveLogicalDevice, remove: deleteLogicalDevice, explanations: getLogicalDeviceExplanations }

function sourceKey(providerId: string, deviceId: string) { return `${providerId}\u0000${deviceId}` }
function candidateLabel(candidate: LogicalDeviceCandidate) { return `${candidate.left.name} · ${candidate.left.providerId} ↔ ${candidate.right.providerId}` }
function emptyConfig(devices: Device[]): LogicalDeviceConfig {
  const first = devices[0]
  return { id: '', name: '', type: first?.type ?? 'switch', bindings: [] }
}

export function LogicalDeviceManager({ devices, onClose, onChanged, api = defaultAPI }: { devices: Device[]; onClose: () => void; onChanged: () => Promise<void>; api?: LogicalDeviceAPI }) {
  const concreteDevices = useMemo(() => devices.filter((item) => item.providerId !== 'logical' && !item.removed), [devices])
  const [items, setItems] = useState<LogicalDeviceConfig[]>([])
  const [candidates, setCandidates] = useState<LogicalDeviceCandidate[]>([])
  const [editing, setEditing] = useState<LogicalDeviceConfig | null>(null)
  const [firstSource, setFirstSource] = useState('')
  const [secondSource, setSecondSource] = useState('')
  const [routesJSON, setRoutesJSON] = useState('{\n  "propertyRoutes": [],\n  "commandRoutes": []\n}')
  const [explanations, setExplanations] = useState<LogicalRouteExplanation[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function load() {
    const [configs, suggestions] = await Promise.all([api.list(), api.candidates().catch(() => [])])
    setItems(configs); setCandidates(suggestions)
  }
  useEffect(() => { void load().catch((cause) => setError(cause instanceof Error ? cause.message : '加载设备链接失败')) }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const selected = (value: string) => concreteDevices.find((item) => sourceKey(item.providerId, item.id) === value)
  function edit(item?: LogicalDeviceConfig) {
    const next = item ? structuredClone(item) : emptyConfig(concreteDevices)
    setEditing(next)
    setFirstSource(next.bindings[0] ? sourceKey(next.bindings[0].providerId, next.bindings[0].deviceId) : '')
    setSecondSource(next.bindings[1] ? sourceKey(next.bindings[1].providerId, next.bindings[1].deviceId) : '')
    setRoutesJSON(JSON.stringify({ propertyRoutes: next.propertyRoutes ?? [], commandRoutes: next.commandRoutes ?? [] }, null, 2))
    setExplanations([]); setError(null)
  }
  function applyCandidate(candidate: LogicalDeviceCandidate) {
    const left = sourceKey(candidate.left.providerId, candidate.left.deviceId)
    const right = sourceKey(candidate.right.providerId, candidate.right.deviceId)
    setEditing({ id: '', name: candidate.left.name, type: candidate.left.type, bindings: [] })
    setFirstSource(left); setSecondSource(right); setRoutesJSON('{\n  "propertyRoutes": [],\n  "commandRoutes": []\n}'); setError(null)
  }
  async function save() {
    if (!editing) return
    const first = selected(firstSource); const second = selected(secondSource)
    if (!first || !second || firstSource === secondSource) { setError('请选择两个不同的 Provider 设备来源。'); return }
    let routes: Pick<LogicalDeviceConfig, 'propertyRoutes' | 'commandRoutes'>
    try { routes = JSON.parse(routesJSON) as Pick<LogicalDeviceConfig, 'propertyRoutes' | 'commandRoutes'> } catch { setError('高级路由 JSON 无法解析。'); return }
    const next: LogicalDeviceConfig = { ...editing, type: first.type, bindings: [{ providerId: first.providerId, deviceId: first.id, priority: 0 }, { providerId: second.providerId, deviceId: second.id, priority: 10 }], propertyRoutes: routes.propertyRoutes ?? [], commandRoutes: routes.commandRoutes ?? [] }
    setBusy(true); setError(null)
    try {
      await api.save(next, items.some((item) => item.id === next.id))
      setEditing(null); await load(); await onChanged()
    } catch (cause) { setError(cause instanceof Error ? cause.message : '保存设备链接失败') } finally { setBusy(false) }
  }
  async function remove(item: LogicalDeviceConfig) {
    if (!window.confirm(`解绑逻辑设备“${item.name}”？这会恢复两个 Provider 的独立设备卡片。`)) return
    setBusy(true); setError(null)
    try { await api.remove(item.id); await load(); await onChanged() } catch (cause) { setError(cause instanceof Error ? cause.message : '解绑失败') } finally { setBusy(false) }
  }
  async function inspect(item: LogicalDeviceConfig) {
    setError(null)
    try { setExplanations(await api.explanations(item.id)) } catch (cause) { setError(cause instanceof Error ? cause.message : '读取路由解释失败') }
  }

  return <div className="modal-backdrop is-mapping is-centered"><section className="logical-device-manager device-mapping-dialog" role="dialog" aria-modal="true" aria-label="设备链接与多 Provider 路由">
    <div className="form-heading"><div><p className="eyebrow">LOGICAL DEVICE · MULTI-PROVIDER</p><h2><HelpTooltip content="同名设备不会自动合并；属性按优先级回退，命令仅在幂等且前一来源不可用时回退。" label="逻辑设备说明">设备链接与多 Provider 路由</HelpTooltip></h2></div><button onClick={onClose}>关闭</button></div>
    {error && <p className="inline-error" role="alert">{error}</p>}
    <section className="config-note"><span>自动候选</span><strong><HelpTooltip content="候选需同类型、规范化名称相同且家庭和房间相同。" label="自动候选说明">可手动确认</HelpTooltip></strong><p>{candidates.length ? candidates.map((candidate) => <button key={`${candidate.left.providerId}/${candidate.left.deviceId}/${candidate.right.providerId}/${candidate.right.deviceId}`} type="button" onClick={() => applyCandidate(candidate)}>使用候选：{candidateLabel(candidate)}</button>) : '暂无候选'}</p></section>
    <div className="logical-device-layout"><section><div className="profile-heading"><div><p className="eyebrow">已链接设备</p><h3>Logical Devices</h3></div><button className="add-button" type="button" onClick={() => edit()}>＋ 新建设备链接</button></div>
      {items.map((item) => <article className="logical-device-item" key={item.id}><div><strong>{item.name}</strong><code>{item.id} · {item.type}</code><small>{item.bindings.map((binding) => `${binding.providerId}/${binding.deviceId} (P${binding.priority})`).join(' → ')}</small></div><div><button type="button" onClick={() => edit(item)}>编辑</button><button type="button" onClick={() => void inspect(item)}>路由解释</button><button type="button" className="danger-link" disabled={busy} onClick={() => void remove(item)}>解绑</button></div></article>)}
      {items.length === 0 && <p className="empty-state"><HelpTooltip content="选择两个已发现的来源设备后，手动确认链接。" label="新建设备链接说明">暂无逻辑设备</HelpTooltip></p>}
    </section>
    {editing && <section className="logical-device-editor"><div className="profile-heading"><div><p className="eyebrow">手动链接</p><h3>{items.some((item) => item.id === editing.id) ? '编辑逻辑设备' : '新建设备链接'}</h3></div><button type="button" onClick={() => setEditing(null)}>取消</button></div>
      <label>逻辑设备 ID<input aria-label="逻辑设备 ID" disabled={items.some((item) => item.id === editing.id)} value={editing.id} onChange={(event) => setEditing({ ...editing, id: event.target.value })} placeholder="living-switch" /></label>
      <label>显示名称<input aria-label="逻辑设备名称" value={editing.name} onChange={(event) => setEditing({ ...editing, name: event.target.value })} placeholder="客厅主灯" /></label>
      <label>主 Provider 设备<select aria-label="主 Provider 设备" value={firstSource} onChange={(event) => setFirstSource(event.target.value)}><option value="">选择主来源</option>{concreteDevices.map((item) => <option key={sourceKey(item.providerId, item.id)} value={sourceKey(item.providerId, item.id)}>{item.name} · {item.providerId}/{item.id} · {item.type}</option>)}</select></label>
      <label>回退 Provider 设备<select aria-label="回退 Provider 设备" value={secondSource} onChange={(event) => setSecondSource(event.target.value)}><option value="">选择回退来源</option>{concreteDevices.map((item) => <option key={sourceKey(item.providerId, item.id)} value={sourceKey(item.providerId, item.id)}>{item.name} · {item.providerId}/{item.id} · {item.type}</option>)}</select></label>
      <label><HelpTooltip content="省略路由时，按来源优先级匹配相同地址；allowFallback 只会在前一来源不可用时启用，非幂等命令不会回退。" label="高级路由说明">高级路由 JSON</HelpTooltip><textarea aria-label="高级属性命令路由 JSON" value={routesJSON} onChange={(event) => setRoutesJSON(event.target.value)} rows={10} spellCheck={false} /></label>
      <button className="add-button" type="button" disabled={busy || !editing.id.trim() || !editing.name.trim()} onClick={() => void save()}>{busy ? '保存中…' : '保存链接'}</button>
    </section>}
    </div>
    {explanations.length > 0 && <section className="logical-route-explanations"><h3>当前路由解释</h3>{explanations.map((item) => <p key={`${item.kind}/${item.path}`}><code>{item.kind} · {item.path}</code>：{item.reason} → {item.selected.providerId}/{item.selected.deviceId}（P{item.selected.priority}，{item.selected.available ? '可用' : '不可用'}）</p>)}</section>}
  </section></div>
}
