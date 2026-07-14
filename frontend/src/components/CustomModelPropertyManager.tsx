import { useCallback, useEffect, useState } from 'react'
import { createCustomModelProperty, deleteCustomModelProperty, listCustomModelProperties, updateCustomModelProperty } from '../api/mapping'
import type { DeviceType, ValueType } from '../types/device'
import type { CustomModelProperty } from '../types/mapping'

const deviceTypes: DeviceType[] = ['switch', 'lightbulb', 'outlet', 'temperature-sensor', 'humidity-sensor', 'contact-sensor', 'motion-sensor', 'fan', 'air-purifier', 'window-covering']
const valueTypes: ValueType[] = ['bool', 'int', 'number', 'string', 'enum']

function emptyProperty(): CustomModelProperty {
  return {
    id: '', deviceType: 'switch', endpointId: 'main', endpointName: '主端点', endpointType: 'main',
    capabilityId: 'custom', capabilityType: 'custom',
    definition: { id: '', name: '', type: 'string', parameterLevel: 'custom', readable: true, writable: false, notifiable: true },
  }
}

export function CustomModelPropertyManager({ onChanged }: { onChanged: () => void }) {
  const [items, setItems] = useState<CustomModelProperty[]>([])
  const [editing, setEditing] = useState<CustomModelProperty | null>(null)
  const [originalID, setOriginalID] = useState('')
  const [enumText, setEnumText] = useState('')
  const [error, setError] = useState('')
  const refresh = useCallback(async () => { try { setItems(await listCustomModelProperties()); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '读取自定义属性失败') } }, [])
  useEffect(() => { void refresh() }, [refresh])
  const edit = (item?: CustomModelProperty) => { const next = item ? structuredClone(item) : emptyProperty(); setEditing(next); setOriginalID(item?.id ?? ''); setEnumText(next.definition.enum?.join(', ') ?? ''); setError('') }
  const patchDefinition = (patch: Partial<CustomModelProperty['definition']>) => setEditing((current) => current ? ({ ...current, definition: { ...current.definition, ...patch } }) : current)
  const save = async () => {
    if (!editing) return
    const next = structuredClone(editing)
    next.definition.parameterLevel = 'custom'
    next.definition.enum = next.definition.type === 'enum' ? enumText.split(',').map((item) => item.trim()).filter(Boolean) : undefined
    try {
      if (originalID) await updateCustomModelProperty(originalID, next); else await createCustomModelProperty(next)
      setEditing(null); await refresh(); onChanged()
    } catch (cause) { setError(cause instanceof Error ? cause.message : '保存自定义属性失败') }
  }
  const remove = async (item: CustomModelProperty) => { if (!window.confirm(`删除自定义属性 ${item.definition.name}？`)) return; try { await deleteCustomModelProperty(item.id); await refresh(); onChanged() } catch (cause) { setError(cause instanceof Error ? cause.message : '删除自定义属性失败') } }

  return <section className="custom-model-manager">
    <div className="profile-heading"><div><p className="eyebrow">CUSTOM UNIFIED PROPERTIES</p><h3>统一模型自定义属性</h3><p>自定义属性同样使用 Endpoint / Capability / Property 三级地址，并保存完整的类型、单位、范围和 R/W/N 权限。</p></div><button onClick={() => edit()}>＋ 新建自定义属性</button></div>
    {error && <p className="inline-error" role="alert">{error}</p>}
    <div className="custom-property-list">{items.map((item) => <article key={item.id}><span className="parameter-level is-custom">custom</span><div><strong>{item.definition.name}</strong><code>{item.deviceType} / {item.endpointId} / {item.capabilityId} / {item.definition.id}</code><small>{item.definition.type}{item.definition.unit ? ` · ${item.definition.unit}` : ''} · {item.definition.readable ? 'R' : '–'}{item.definition.writable ? 'W' : '–'}{item.definition.notifiable ? 'N' : '–'}</small></div><div><button onClick={() => edit(item)}>编辑</button><button className="danger-link" onClick={() => void remove(item)}>删除</button></div></article>)}</div>
    {items.length === 0 && !editing && <div className="empty-state">还没有自定义模型属性。标准模型之外的厂商能力可以从这里显式加入中间层。</div>}
    {editing && <div className="custom-property-editor" role="dialog" aria-label="自定义统一模型属性"><div className="form-heading"><div><p className="eyebrow">THREE-LEVEL PROPERTY</p><h3>{originalID ? '编辑自定义属性' : '新建自定义属性'}</h3></div><button onClick={() => setEditing(null)}>关闭</button></div>
      <div className="custom-property-grid">
        <label>配置 ID<input value={editing.id} disabled={Boolean(originalID)} onChange={(event) => setEditing({ ...editing, id: event.target.value })} placeholder="custom-air-co2" /></label>
        <label>设备模型<select value={editing.deviceType} onChange={(event) => setEditing({ ...editing, deviceType: event.target.value as DeviceType })}>{deviceTypes.map((item) => <option key={item}>{item}</option>)}</select></label>
        <fieldset><legend>第一级 · Endpoint</legend><label>ID<input value={editing.endpointId} onChange={(event) => setEditing({ ...editing, endpointId: event.target.value })} /></label><label>名称<input value={editing.endpointName} onChange={(event) => setEditing({ ...editing, endpointName: event.target.value })} /></label><label>类型<input value={editing.endpointType} onChange={(event) => setEditing({ ...editing, endpointType: event.target.value })} /></label></fieldset>
        <fieldset><legend>第二级 · Capability</legend><label>ID<input value={editing.capabilityId} onChange={(event) => setEditing({ ...editing, capabilityId: event.target.value })} /></label><label>类型<input value={editing.capabilityType} onChange={(event) => setEditing({ ...editing, capabilityType: event.target.value })} /></label></fieldset>
        <fieldset><legend>第三级 · Property</legend><label>ID<input value={editing.definition.id} onChange={(event) => patchDefinition({ id: event.target.value })} /></label><label>名称<input value={editing.definition.name} onChange={(event) => patchDefinition({ name: event.target.value })} /></label><label>类型<select value={editing.definition.type} onChange={(event) => patchDefinition({ type: event.target.value as ValueType })}>{valueTypes.map((item) => <option key={item}>{item}</option>)}</select></label><label>单位<input value={editing.definition.unit ?? ''} onChange={(event) => patchDefinition({ unit: event.target.value || undefined })} /></label>{editing.definition.type === 'enum' && <label>枚举值<input value={enumText} onChange={(event) => setEnumText(event.target.value)} placeholder="auto, manual" /></label>}<label>最小值<input type="number" value={editing.definition.min ?? ''} onChange={(event) => patchDefinition({ min: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><label>最大值<input type="number" value={editing.definition.max ?? ''} onChange={(event) => patchDefinition({ max: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><label>步长<input type="number" value={editing.definition.step ?? ''} onChange={(event) => patchDefinition({ step: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><div className="permission-checks"><label><input type="checkbox" checked={editing.definition.readable} onChange={(event) => patchDefinition({ readable: event.target.checked })} />可读 R</label><label><input type="checkbox" checked={editing.definition.writable} onChange={(event) => patchDefinition({ writable: event.target.checked })} />可写 W</label><label><input type="checkbox" checked={editing.definition.notifiable} onChange={(event) => patchDefinition({ notifiable: event.target.checked })} />通知 N</label></div></fieldset>
      </div><button className="add-button" onClick={() => void save()}>保存自定义属性</button>
    </div>}
  </section>
}
