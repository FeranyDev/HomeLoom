import { useCallback, useEffect, useRef, useState } from 'react'
import { createCustomModelProperty, deleteCustomModelProperty, listCustomModelProperties, updateCustomModelProperty } from '../api/mapping'
import { builtInDeviceTypes, type DeviceType, type ValueType } from '../types/device'
import type { CustomModelProperty } from '../types/mapping'
import { deviceTypeLabel, parameterLevelLabel, permissionLabel, propertyDisplayLabel, unitLabel, valueTypeLabel } from '../presentationLabels'
import { HelpTooltip } from './HelpTooltip'

const deviceTypes: DeviceType[] = [...builtInDeviceTypes]
const valueTypes: ValueType[] = ['bool', 'int', 'number', 'string', 'enum']

function emptyProperty(deviceType: DeviceType = 'switch'): CustomModelProperty {
  return {
    id: '', deviceType, endpointId: 'main', endpointName: '主端点', endpointType: 'main',
    capabilityId: 'custom', capabilityType: 'custom',
    definition: { id: '', name: '', type: 'string', parameterLevel: 'custom', readable: true, writable: false, notifiable: true },
  }
}

interface CustomModelPropertyManagerProps {
  onChanged: () => void
  deviceType?: DeviceType
  embedded?: boolean
  createRevision?: number
}

export function CustomModelPropertyManager({ onChanged, deviceType, embedded = false, createRevision = 0 }: CustomModelPropertyManagerProps) {
  const [items, setItems] = useState<CustomModelProperty[]>([])
  const [editing, setEditing] = useState<CustomModelProperty | null>(null)
  const [originalID, setOriginalID] = useState('')
  const [enumText, setEnumText] = useState('')
  const [error, setError] = useState('')
  const handledCreateRevision = useRef(0)
  const visibleItems = deviceType ? items.filter((item) => item.deviceType === deviceType) : items
  const refresh = useCallback(async () => { try { setItems(await listCustomModelProperties()); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '读取自定义属性失败') } }, [])
  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => { setEditing(null); setOriginalID('') }, [deviceType])
  useEffect(() => {
    if (createRevision < 1 || handledCreateRevision.current === createRevision) return
    handledCreateRevision.current = createRevision
    const next = emptyProperty(deviceType)
    setEditing(next); setOriginalID(''); setEnumText(''); setError('')
  }, [createRevision, deviceType])
  const edit = (item?: CustomModelProperty) => { const next = item ? structuredClone(item) : emptyProperty(deviceType); setEditing(next); setOriginalID(item?.id ?? ''); setEnumText(next.definition.enum?.join(', ') ?? ''); setError('') }
  const patchDefinition = (patch: Partial<CustomModelProperty['definition']>) => setEditing((current) => current ? ({ ...current, definition: { ...current.definition, ...patch } }) : current)
  const changeValueType = (type: ValueType) => {
    const numeric = type === 'int' || type === 'number'
    setEditing((current) => {
      if (!current) return current
      const definition = { ...current.definition, type }
      if (!numeric) {
        definition.unit = undefined
        definition.min = undefined
        definition.max = undefined
        definition.step = undefined
      }
      if (type !== 'enum') definition.enum = undefined
      return { ...current, definition }
    })
    if (type !== 'enum') setEnumText('')
  }
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

  return <section className={`custom-model-manager ${embedded ? 'is-embedded' : ''}`}>
    <div className="profile-heading"><div><p className="eyebrow">统一模型自定义属性（CUSTOM UNIFIED PROPERTIES）</p><h3><HelpTooltip label="自定义属性说明" content="使用端点、能力、属性三级地址定义类型、单位、范围和读写权限。保存后可用于设备映射。">{deviceType ? `${deviceTypeLabel(deviceType)} · 自定义属性` : '自定义属性'}</HelpTooltip></h3></div>{!embedded && <button className="add-button" onClick={() => edit()}>＋ 新建属性</button>}</div>
    {error && <p className="inline-error" role="alert">{error}</p>}
    <div className="custom-property-list">{visibleItems.map((item) => <article key={item.id}><span className="parameter-level is-custom">{parameterLevelLabel('custom')}</span><div><strong>{propertyDisplayLabel(item.definition.name, item.definition.id)}</strong><code>{item.deviceType} / {item.endpointId} / {item.capabilityId} / {item.definition.id}</code><small>{deviceTypeLabel(item.deviceType)} · {valueTypeLabel(item.definition.type)}{item.definition.unit ? ` · 单位 ${unitLabel(item.definition.unit)}` : ''} · {permissionLabel(item.definition.readable, item.definition.writable, item.definition.notifiable)}</small></div><div><button onClick={() => edit(item)}>编辑</button><button className="danger-link" onClick={() => void remove(item)}>删除</button></div></article>)}</div>
    {visibleItems.length === 0 && !editing && <div className="empty-state">{deviceType ? '这个模型还没有自定义属性。' : '还没有自定义模型属性。'}标准模型之外的厂商能力可以从这里显式加入中间层。</div>}
    {editing && <div className="custom-property-editor" role="dialog" aria-label="自定义统一模型属性"><div className="form-heading"><div><p className="eyebrow">三级属性（THREE-LEVEL PROPERTY）</p><h3>{originalID ? '编辑自定义属性' : '新建自定义属性'}</h3></div><button onClick={() => setEditing(null)}>关闭</button></div>
      <div className="custom-property-grid">
        <label>配置标识（ID）<input value={editing.id} disabled={Boolean(originalID)} onChange={(event) => setEditing({ ...editing, id: event.target.value })} placeholder="custom-air-co2" /></label>
        <label>设备模型（deviceType）<select value={editing.deviceType} disabled={Boolean(deviceType)} onChange={(event) => setEditing({ ...editing, deviceType: event.target.value as DeviceType })}>{!deviceTypes.includes(editing.deviceType) && <option value={editing.deviceType}>{deviceTypeLabel(editing.deviceType)}</option>}{deviceTypes.map((item) => <option key={item} value={item}>{deviceTypeLabel(item)}</option>)}</select></label>
        <fieldset><legend>第一级 · 端点（Endpoint）</legend><label>标识（ID）<input value={editing.endpointId} onChange={(event) => setEditing({ ...editing, endpointId: event.target.value })} /></label><label>名称（name）<input value={editing.endpointName} onChange={(event) => setEditing({ ...editing, endpointName: event.target.value })} /></label><label>类型（type）<input value={editing.endpointType} onChange={(event) => setEditing({ ...editing, endpointType: event.target.value })} /></label></fieldset>
        <fieldset><legend>第二级 · 能力（Capability）</legend><label>标识（ID）<input value={editing.capabilityId} onChange={(event) => setEditing({ ...editing, capabilityId: event.target.value })} /></label><label>类型（type）<input value={editing.capabilityType} onChange={(event) => setEditing({ ...editing, capabilityType: event.target.value })} /></label></fieldset>
        <fieldset><legend>第三级 · 属性（Property）</legend><label>标识（ID）<input value={editing.definition.id} onChange={(event) => patchDefinition({ id: event.target.value })} /></label><label>名称（name）<input value={editing.definition.name} onChange={(event) => patchDefinition({ name: event.target.value })} /></label><label>值类型（type）<select value={editing.definition.type} onChange={(event) => changeValueType(event.target.value as ValueType)}>{valueTypes.map((item) => <option key={item} value={item}>{valueTypeLabel(item)}</option>)}</select></label>{(editing.definition.type === 'int' || editing.definition.type === 'number') && <><label>单位（unit）<input value={editing.definition.unit ?? ''} onChange={(event) => patchDefinition({ unit: event.target.value || undefined })} /></label><label>最小值（min）<input type="number" value={editing.definition.min ?? ''} onChange={(event) => patchDefinition({ min: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><label>最大值（max）<input type="number" value={editing.definition.max ?? ''} onChange={(event) => patchDefinition({ max: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><label>步长（step）<input type="number" value={editing.definition.step ?? ''} onChange={(event) => patchDefinition({ step: event.target.value === '' ? undefined : Number(event.target.value) })} /></label></>}{editing.definition.type === 'enum' && <label>枚举值（enum）<input value={enumText} onChange={(event) => setEnumText(event.target.value)} placeholder="auto, manual" /></label>}<label>状态过期秒数（staleAfterSeconds）<input type="number" min="0" value={editing.definition.staleAfterSeconds ?? ''} onChange={(event) => patchDefinition({ staleAfterSeconds: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><div className="permission-checks"><label><input type="checkbox" checked={editing.definition.readable} onChange={(event) => patchDefinition({ readable: event.target.checked })} />可读（R）</label><label><input type="checkbox" checked={editing.definition.writable} onChange={(event) => patchDefinition({ writable: event.target.checked })} />可写（W）</label><label><input type="checkbox" checked={editing.definition.notifiable} onChange={(event) => patchDefinition({ notifiable: event.target.checked })} />通知（N）</label></div></fieldset>
      </div><button className="add-button" onClick={() => void save()}>保存自定义属性</button>
    </div>}
  </section>
}
