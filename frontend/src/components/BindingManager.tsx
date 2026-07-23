import { useCallback, useEffect, useMemo, useState } from 'react'
import * as mappingApi from '../api/mapping'
import type { Device, DeviceType, PropertyDefinition, PropertyValue } from '../types/device'
import type { ConsumerProperty, MappingBinding, MappingCatalog, MappingProfileInfo, SourceCatalogDevice, SourceCatalogMetadata, SourceValueStatus } from '../types/mapping'
import { consumerPropertyLabel, deviceTypeLabel, matterClusterLabel, matterConsumerPathLabel, parameterLevelLabel, permissionLabel, propertyDisplayLabel, valueTypeLabel } from '../presentationLabels'

type ProviderProperty = {
  key: string; providerId: string; deviceId: string; deviceName: string; deviceType: DeviceType
  endpointId: string; capabilityId: string; propertyId: string; definition: PropertyDefinition
  value: PropertyValue; valueStatus: SourceValueStatus
}

type DefaultProviderRoute = {
  key: string
  source: ProviderProperty
  model: MappingCatalog['models'][number]['parameters'][number]
}

type EnumCompatibility = {
  kind: 'none' | 'exact' | 'normalized' | 'partial' | 'requires-profile'
  source: string[]; target: string[]; pairs: Array<{ source: string; target: string }>
  sourceOnly: string[]; targetOnly: string[]
}

type BindingAPI = {
  listBindings: typeof mappingApi.listMappingBindings; listProfiles: typeof mappingApi.listMappingProfiles
  create: typeof mappingApi.createMappingBinding; update: typeof mappingApi.updateMappingBinding
  remove: typeof mappingApi.deleteMappingBinding; catalog: typeof mappingApi.getMappingCatalog
}

const defaultAPI: BindingAPI = {
  listBindings: mappingApi.listMappingBindings, listProfiles: mappingApi.listMappingProfiles,
  create: mappingApi.createMappingBinding, update: mappingApi.updateMappingBinding,
  remove: mappingApi.deleteMappingBinding, catalog: mappingApi.getMappingCatalog,
}

const pathKey = (path: { endpointId: string; capabilityId: string; propertyId: string }) => `${path.endpointId}/${path.capabilityId}/${path.propertyId}`
const routeError = (cause: unknown) => cause instanceof Error ? cause.message : '映射路由操作失败'
const canonicalEnumToken = (value: string) => value.trim().toLowerCase().replace(/[\s_]+/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '')

function matterMember(property: ConsumerProperty): { cluster: string; member: string; kind: 'attribute' | 'command' } {
	const segments = property.id.split('.')
	return { cluster: property.cluster ?? segments[0] ?? 'UnknownCluster', member: property.element ?? property.member ?? (segments.slice(1).join('.') || property.id), kind: property.kind ?? 'attribute' }
}

function orderModelParameters(parameters: MappingCatalog['models'][number]['parameters']) {
  const capabilities = new Map<string, typeof parameters>()
  for (const parameter of parameters) {
    const key = `${parameter.path.endpointId}/${parameter.path.capabilityId}`
    capabilities.set(key, [...(capabilities.get(key) ?? []), parameter])
  }
  return [...capabilities.values()]
    .sort((left, right) => Number(right.some((item) => item.level === 'required')) - Number(left.some((item) => item.level === 'required')))
    .flatMap((items) => [...items].sort((left, right) => Number(right.level === 'required') - Number(left.level === 'required')))
}

function compareEnumDomains(sourceDefinition?: PropertyDefinition, modelParameter?: MappingCatalog['models'][number]['parameters'][number]): EnumCompatibility {
  const source = sourceDefinition?.type === 'enum' ? sourceDefinition.enum ?? [] : []
  const target = modelParameter?.type === 'enum' ? modelParameter.enum ?? [] : []
  if (sourceDefinition?.type !== 'enum' || modelParameter?.type !== 'enum' || source.length === 0 || target.length === 0) {
    return { kind: 'none', source, target, pairs: [], sourceOnly: [], targetOnly: [] }
  }
  const targetByCanonical = new Map<string, string[]>()
  target.forEach((item) => targetByCanonical.set(canonicalEnumToken(item), [...(targetByCanonical.get(canonicalEnumToken(item)) ?? []), item]))
  const sourceCounts = new Map<string, number>()
  source.forEach((item) => sourceCounts.set(canonicalEnumToken(item), (sourceCounts.get(canonicalEnumToken(item)) ?? 0) + 1))
  const pairs: EnumCompatibility['pairs'] = []
  const sourceOnly: string[] = []
  for (const item of source) {
    const matches = targetByCanonical.get(canonicalEnumToken(item)) ?? []
    if (matches.length === 1 && sourceCounts.get(canonicalEnumToken(item)) === 1) pairs.push({ source: item, target: matches[0] })
    else sourceOnly.push(item)
  }
  const matchedTargets = new Set(pairs.map((item) => item.target))
  const targetOnly = target.filter((item) => !matchedTargets.has(item))
  if (sourceOnly.length > 0) return { kind: 'requires-profile', source, target, pairs, sourceOnly, targetOnly }
  if (targetOnly.length > 0) return { kind: 'partial', source, target, pairs, sourceOnly, targetOnly }
  const exact = pairs.every((item) => item.source === item.target)
  return { kind: exact ? 'exact' : 'normalized', source, target, pairs, sourceOnly, targetOnly }
}

function providerProperties(devices: Device[], metadata: SourceCatalogMetadata): ProviderProperty[] {
  const inferKnown = metadata.source === 'provider-discovery' || metadata.source === 'device-snapshot' || metadata.source === 'unified-registry-fallback'
  return devices.flatMap((item) => item.endpoints.flatMap((endpoint) => endpoint.capabilities.flatMap((capability) => capability.properties.map((property) => ({
    key: `${item.providerId}/${item.id}/${endpoint.id}/${capability.id}/${property.definition.id}`,
    providerId: item.providerId, deviceId: item.id, deviceName: item.name, deviceType: item.type,
    endpointId: endpoint.id, capabilityId: capability.id, propertyId: property.definition.id, definition: property.definition,
    value: property.value,
    valueStatus: metadata.values?.[`${endpoint.id}/${capability.id}/${property.definition.id}`] ?? { known: inferKnown, available: inferKnown && item.online, observedAt: inferKnown ? item.lastUpdateAt : undefined },
  })))))
}

function propertyValueText(value: PropertyValue): string {
  if (value.bool !== undefined) return value.bool ? 'true' : 'false'
  if (value.int !== undefined) return String(value.int)
  if (value.number !== undefined) return Number.isFinite(value.number) ? String(value.number) : '—'
  return value.string ?? '—'
}

export function BindingManager({ device, profileRevision = 0, catalogRevision = 0, api = defaultAPI, initialStage = 'provider', providerOnly = false, consumerOnly = false, consumerLabel, targetId, consumerDeviceId, consumerId, consumerDeviceType }: {
  device: Device; profileRevision?: number; catalogRevision?: number; api?: BindingAPI
  initialStage?: 'provider' | 'consumer'; providerOnly?: boolean; consumerOnly?: boolean; consumerLabel?: string; targetId?: string; consumerDeviceId?: string; consumerId?: string; consumerDeviceType?: DeviceType
}) {
  const [stage, setStage] = useState<'provider' | 'consumer'>(consumerOnly ? 'consumer' : initialStage)
  const fallbackMetadata: SourceCatalogMetadata = { complete: false, source: 'device-snapshot', error: '原始属性目录尚未加载' }
  const fallbackCatalogDevice: SourceCatalogDevice = { ...device, catalog: fallbackMetadata }
  const [catalog, setCatalog] = useState<MappingCatalog>({ providers: [fallbackCatalogDevice], models: [], consumers: [] })
  const [bindings, setBindings] = useState<MappingBinding[]>([])
  const [profiles, setProfiles] = useState<MappingProfileInfo[]>([])
  const [sourceKey, setSourceKey] = useState('')
  const [modelKey, setModelKey] = useState('')
  const [consumerKey, setConsumerKey] = useState('')
  const [profileId, setProfileId] = useState('')
  const [editingID, setEditingID] = useState<string | null>(null)
  const [editingDefaultKey, setEditingDefaultKey] = useState<string | null>(null)
  const [editingEnabled, setEditingEnabled] = useState(true)
  const [lockedProviderSourceKey, setLockedProviderSourceKey] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [nextBindings, nextProfiles, nextCatalog] = await Promise.all([api.listBindings(), api.listProfiles(), api.catalog()])
      setBindings(nextBindings.filter((item) => item.providerId === device.providerId && item.deviceId === device.id && (consumerOnly ? item.stage === 'consumer' && item.targetId === targetId && item.consumerDeviceId === consumerDeviceId && (!consumerId || item.consumerId === consumerId) : providerOnly ? item.stage === 'provider' : !item.targetId && !item.consumerDeviceId))); setProfiles(nextProfiles); setCatalog(nextCatalog); setError('')
    } catch (cause) { setError(routeError(cause)) }
  }, [api, consumerDeviceId, consumerId, consumerOnly, device.id, device.providerId, providerOnly, targetId])
  useEffect(() => { void refresh() }, [profileRevision, catalogRevision, refresh])

  const catalogDevice = catalog.providers.find((item) => item.providerId === device.providerId && item.id === device.id) ?? fallbackCatalogDevice
  const catalogMetadata = catalogDevice.catalog ?? fallbackMetadata
  const sources = useMemo(() => providerProperties([catalogDevice], catalogMetadata), [catalogDevice, catalogMetadata])
  const sourceCommands = catalogDevice.endpoints.flatMap((endpoint) => endpoint.capabilities.flatMap((capability) => capability.commands?.map((command) => ({ endpoint, capability, command })) ?? []))
  const sourceEvents = catalogDevice.endpoints.flatMap((endpoint) => endpoint.capabilities.flatMap((capability) => capability.events?.map((event) => ({ endpoint, capability, event })) ?? []))
  const source = sources.find((item) => item.key === sourceKey)
  const consumerDevice = catalogDevice
  const effectiveType = stage === 'provider' ? source?.deviceType ?? device.type : consumerDevice.type
  const targetConsumerType = consumerDeviceType ?? consumerDevice.type
  const model = catalog.models.find((item) => item.deviceType === effectiveType)
  const parameters = useMemo(() => orderModelParameters(model?.parameters ?? []), [model])
  const modelParameter = parameters.find((item) => pathKey(item.path) === modelKey)
  const consumerCatalogs = consumerId ? catalog.consumers.filter((item) => item.id === consumerId) : catalog.consumers
  const consumers = consumerCatalogs.flatMap((item) => item.properties.map((property) => ({ consumer: item, property }))).filter((item) => item.property.deviceType === targetConsumerType)
	const matterConsumerGroups = consumers.filter((item) => item.consumer.id === 'matter').reduce<Array<{ cluster: string; items: typeof consumers }>>((groups, item) => {
		const cluster = matterMember(item.property).cluster
		const group = groups.find((candidate) => candidate.cluster === cluster)
		if (group) group.items.push(item)
		else groups.push({ cluster, items: [item] })
		return groups
	}, [])
  const otherConsumers = consumers.filter((item) => item.consumer.id !== 'matter')
  const consumer = consumers.find((item) => `${item.consumer.id}/${item.property.id}` === consumerKey)
  const inputType = stage === 'provider' ? source?.definition.type : modelParameter?.type
  const outputType = stage === 'provider' ? modelParameter?.type : consumer?.property.type
  const enumCompatibility = compareEnumDomains(stage === 'provider' ? source?.definition : undefined, stage === 'provider' ? modelParameter : undefined)
  const enumProfileRequired = stage === 'provider' && enumCompatibility.kind === 'requires-profile' && !profileId
  const compatibleProfiles = profiles.filter((item) => item.inputType === inputType && item.outputType === outputType && !item.transforms.some((transform) => transform.type === 'clamp') && (stage === 'provider' ? item.kind !== 'target' : item.kind !== 'provider'))
  const defaultProviderRoutes = useMemo<DefaultProviderRoute[]>(() => {
    const explicitTargets = new Set(bindings.filter((item) => item.stage === 'provider' && item.enabled).map((item) => pathKey({ endpointId: item.modelEndpointId, capabilityId: item.modelCapabilityId, propertyId: item.modelPropertyId })))
    const modelParameters = catalog.models.find((item) => item.deviceType === device.type)?.parameters ?? []
    const parametersByPath = new Map(modelParameters.map((item) => [pathKey(item.path), item]))
    return sources.flatMap((item) => {
      const model = parametersByPath.get(pathKey({ endpointId: item.endpointId, capabilityId: item.capabilityId, propertyId: item.propertyId }))
      if (!model || model.type !== item.definition.type || explicitTargets.has(pathKey(model.path))) return []
      return [{ key: item.key, source: item, model }]
    })
  }, [bindings, catalog.models, device.type, sources])
  const visibleDefaultProviderRoutes = stage === 'provider' ? defaultProviderRoutes : []
  const effectiveRouteCount = bindings.filter((item) => item.enabled).length + visibleDefaultProviderRoutes.length
  const displayedRouteCount = bindings.length + visibleDefaultProviderRoutes.length

  useEffect(() => {
    if (!sourceKey && sources[0]) setSourceKey(sources[0].key)
  }, [sourceKey, sources])
  useEffect(() => {
    if (!parameters.some((item) => pathKey(item.path) === modelKey)) setModelKey(parameters[0] ? pathKey(parameters[0].path) : '')
  }, [effectiveType, modelKey, parameters])
  useEffect(() => {
    if (stage === 'consumer' && !consumers.some((item) => `${item.consumer.id}/${item.property.id}` === consumerKey)) {
      const first = consumers[0]; setConsumerKey(first ? `${first.consumer.id}/${first.property.id}` : '')
    }
  }, [consumerKey, consumers, stage])
  useEffect(() => {
    if (profileId && !compatibleProfiles.some((item) => item.id === profileId)) setProfileId('')
  }, [compatibleProfiles, profileId])

  const clearEditing = () => { setEditingID(null); setEditingDefaultKey(null); setEditingEnabled(true); setLockedProviderSourceKey(null) }

  const save = async () => {
    if (!modelParameter || (stage === 'provider' ? !source : !consumer)) return
    setSaving(true); setError('')
    try {
      const common = { stage, profileId: profileId || undefined, modelEndpointId: modelParameter.path.endpointId, modelCapabilityId: modelParameter.path.capabilityId, modelPropertyId: modelParameter.path.propertyId, enabled: editingID ? editingEnabled : true }
      if (stage === 'provider' && source) {
        const input = { ...common, deviceType: source.deviceType, providerId: source.providerId, deviceId: source.deviceId, endpointId: source.endpointId, capabilityId: source.capabilityId, propertyId: source.propertyId }
        if (editingID) await api.update(editingID, { ...input, id: editingID })
        else await api.create(input)
      } else if (consumer && consumerDevice) {
		const input = { ...common, providerId: consumerDevice.providerId, deviceId: consumerDevice.id, deviceType: consumerDevice.type, consumerDeviceType: targetConsumerType, targetId, consumerDeviceId, consumerId: consumer.consumer.id, consumerProperty: consumer.property.id }
        if (editingID) await api.update(editingID, { ...input, id: editingID })
        else await api.create(input)
      }
      clearEditing()
      await refresh()
    } catch (cause) { setError(routeError(cause)) } finally { setSaving(false) }
  }

  const editDefault = (item: DefaultProviderRoute) => {
    setStage('provider'); setSourceKey(item.source.key); setModelKey(pathKey(item.model.path)); setProfileId('')
    setEditingID(null); setEditingDefaultKey(item.key); setEditingEnabled(true); setLockedProviderSourceKey(item.source.key)
  }

  const edit = (item: MappingBinding) => {
    setStage(item.stage)
    if (item.stage === 'provider') {
      const key = `${item.providerId}/${item.deviceId}/${item.endpointId}/${item.capabilityId}/${item.propertyId}`
      setSourceKey(key); setLockedProviderSourceKey(key)
    } else {
      setLockedProviderSourceKey(null)
    }
    if (item.stage === 'consumer') setConsumerKey(`${item.consumerId}/${item.consumerProperty}`)
    setModelKey(pathKey({ endpointId: item.modelEndpointId, capabilityId: item.modelCapabilityId, propertyId: item.modelPropertyId }))
    setProfileId(item.profileId ?? ''); setEditingID(item.id); setEditingDefaultKey(null); setEditingEnabled(item.enabled)
  }

  const toggle = async (item: MappingBinding) => { try { await api.update(item.id, { ...item, enabled: !item.enabled }); await refresh() } catch (cause) { setError(routeError(cause)) } }
  const remove = async (item: MappingBinding) => { if (!window.confirm(`删除映射路由 ${item.id}？`)) return; try { await api.remove(item.id); await refresh() } catch (cause) { setError(routeError(cause)) } }

  return <section className="binding-manager mapping-graph">
    <div className="profile-heading"><div><p className="eyebrow">设备映射（DEVICE MAPPING） · {device.providerId}</p><h3>{consumerLabel ?? (providerOnly ? `${device.name} · 来源属性映射` : `${device.name}的双段属性路由`)}</h3><p>{consumerOnly ? `从来源设备的统一模型属性绑定到当前目标设备的 ${consumerId ?? 'Consumer'} 属性；每条属性可独立选择转换配置（Profile）。` : providerOnly ? '将这台提供端（Provider）设备的完整来源属性绑定到统一模型；路径和类型一致的属性会显示为可编辑的默认映射，保存修改后成为当前设备独立的数据库覆盖。' : '本编辑器只读取和修改当前设备。提供端（Provider）与消费端（Consumer）通过统一模型通信，两段路由可分别转换、启停和热更新。'}</p></div><span>{effectiveRouteCount} / {displayedRouteCount} 生效</span></div>
    {!consumerOnly && !providerOnly && <div className="mapping-stage-tabs" role="tablist"><button className={stage === 'provider' ? 'is-active' : ''} onClick={() => setStage('provider')}>① 提供端（Provider）→ 统一模型</button><button className={stage === 'consumer' ? 'is-active' : ''} onClick={() => setStage('consumer')}>② 统一模型 → 消费端（Consumer）</button></div>}
    {error && <p className="inline-error" role="alert">{error}</p>}
    <div className="mapping-lanes">
      <section className={`mapping-lane ${stage === 'provider' ? '' : 'is-context'}`}><header><span>提供端（PROVIDERS）</span><strong>{catalogMetadata.complete ? '来源完整属性' : '来源属性（不完整）'}</strong><small>{sources.length} 属性 · {sourceCommands.length} 动作（Action）· {sourceEvents.length} 事件（Event）</small><span className={`catalog-status ${catalogMetadata.complete ? 'is-complete' : 'is-incomplete'}`}>{catalogMetadata.complete ? `完整 · ${catalogMetadata.source}` : `不完整 · ${catalogMetadata.source}`}</span>{catalogMetadata.specType && <code>{catalogMetadata.specType}</code>}{catalogMetadata.error && <small className="catalog-error">{catalogMetadata.error}</small>}</header>
        {stage === 'provider' ? <><div className="mapping-node-list">{sources.map((item) => <button key={item.key} className={item.key === sourceKey ? 'is-selected' : ''} disabled={Boolean(lockedProviderSourceKey && item.key !== lockedProviderSourceKey)} title={lockedProviderSourceKey && item.key !== lockedProviderSourceKey ? '编辑覆盖时来源属性保持不变；如需更换来源，请取消编辑后新建路由。' : undefined} onClick={() => setSourceKey(item.key)}><span>{item.deviceName}</span><strong>{propertyDisplayLabel(item.definition.name, item.propertyId)}</strong><em className={`source-current-value ${item.valueStatus.known && item.valueStatus.available ? 'is-current' : item.valueStatus.known ? 'is-stale' : 'is-unknown'}`}>{item.valueStatus.known ? `${item.valueStatus.available ? '当前' : '上次'} ${propertyValueText(item.value)}${item.definition.unit ? ` ${item.definition.unit}` : ''}` : '当前值未知'}</em><code>{item.providerId} / {item.endpointId}.{item.capabilityId}.{item.propertyId}</code><small>{valueTypeLabel(item.definition.type)}{item.definition.unit ? ` · 单位 ${item.definition.unit}` : ''} · {permissionLabel(item.definition.readable, item.definition.writable, item.definition.notifiable)}{item.valueStatus.observedAt ? ` · ${new Date(item.valueStatus.observedAt).toLocaleTimeString('zh-CN')}` : ''}</small>{item.definition.type === 'enum' && item.definition.enum?.length ? <small className="enum-domain-inline">来源枚举：{item.definition.enum.join(' / ')}</small> : null}{item.valueStatus.error && <small className="catalog-error">{item.valueStatus.error}</small>}</button>)}</div>{(sourceCommands.length > 0 || sourceEvents.length > 0) && <details className="source-definition-summary"><summary>查看全部动作（Action）/ 事件（Event）</summary>{sourceCommands.map(({ endpoint, capability, command }) => <div key={`${endpoint.id}/${capability.id}/${command.id}`}><b>动作（Action）· {command.name}</b><code>{endpoint.id}.{capability.id}.{command.id}</code><small>{command.parameters?.length ?? 0} 个输入参数</small></div>)}{sourceEvents.map(({ endpoint, capability, event }) => <div key={`${endpoint.id}/${capability.id}/${event.id}`}><b>事件（Event）· {event.name}</b><code>{endpoint.id}.{capability.id}.{event.id}</code><small>{valueTypeLabel(event.payload)}</small></div>)}</details>}</> : <div className="mapping-context"><b>提供端边界（Provider）</b><p>消费端（Consumer）不直接读取提供端（Provider）字段，避免平台之间形成隐式耦合。</p></div>}
      </section>
      <div className="mapping-arrow"><span>→</span><small>{stage === 'provider' ? profileId || 'identity' : '统一语义'}</small></div>
      <section className="mapping-lane is-model"><header><span>统一模型（UNIFIED MODEL）</span><strong>端点 / 能力 / 属性（Endpoint / Capability / Property）三级基准</strong><label>当前设备<input aria-label="当前映射设备" value={`${device.name} · ${device.providerId} / ${device.id}`} disabled /></label><label>设备模型（deviceType）<select aria-label="统一设备模型" value={effectiveType} disabled>{catalog.models.map((item) => <option key={item.deviceType} value={item.deviceType}>{deviceTypeLabel(item.deviceType)}</option>)}</select></label></header>
        <div className="mapping-node-list">{parameters.map((item) => <button key={pathKey(item.path)} className={pathKey(item.path) === modelKey ? 'is-selected' : ''} onClick={() => setModelKey(pathKey(item.path))}><span className={`parameter-level is-${item.level}`}>{parameterLevelLabel(item.level)}</span><strong>{propertyDisplayLabel(item.name, item.path.propertyId)}</strong><code>{item.path.endpointId} / {item.path.capabilityId} / {item.path.propertyId}</code><small>{valueTypeLabel(item.type)}{item.unit ? ` · 单位 ${item.unit}` : ''} · {permissionLabel(item.readable, item.writable, item.notifiable)}</small>{item.type === 'enum' && item.enum?.length ? <small className="enum-domain-inline">模型枚举：{item.enum.join(' / ')}</small> : null}</button>)}</div>
      </section>
      <div className="mapping-arrow"><span>→</span><small>{stage === 'consumer' ? profileId || 'identity' : '统一状态'}</small></div>
      <section className={`mapping-lane ${stage === 'consumer' ? '' : 'is-context'}`}><header><span>消费端（CONSUMERS）</span><strong>{consumerCatalogs.map((item) => item.name).join(' / ') || consumerId || '目标完整属性'}</strong><small>{consumers.length} 个属性</small></header>
        {stage === 'consumer' ? consumers.length > 0 ? <div className="mapping-node-list">{matterConsumerGroups.map((group) => <section className="matter-cluster-group" key={group.cluster}><header><span>Cluster</span><strong>{matterClusterLabel(group.cluster)}</strong><small>Cluster → Attribute / Command</small></header>{group.items.map((item) => { const key = `${item.consumer.id}/${item.property.id}`; const member = matterMember(item.property); return <button key={key} className={key === consumerKey ? 'is-selected' : ''} onClick={() => setConsumerKey(key)}><span>Matter · {member.kind === 'command' ? 'Command（命令）' : 'Attribute（属性）'}</span><strong>{matterConsumerPathLabel(member.cluster, member.member, member.kind)}</strong><code>{member.cluster} → {member.kind === 'command' ? 'Command' : 'Attribute'} → {member.member}</code><small>{valueTypeLabel(item.property.type)} · {parameterLevelLabel(item.property.level)} · {permissionLabel(item.property.readable, item.property.writable, item.property.notifiable)}</small></button> })}</section>)}{otherConsumers.map((item) => { const key = `${item.consumer.id}/${item.property.id}`; return <button key={key} className={key === consumerKey ? 'is-selected' : ''} onClick={() => setConsumerKey(key)}><span>{item.consumer.name}（{item.consumer.id}）</span><strong>{consumerPropertyLabel(item.property.id)}</strong><code>{item.property.id}</code><small>{valueTypeLabel(item.property.type)} · {parameterLevelLabel(item.property.level)} · {permissionLabel(item.property.readable, item.property.writable, item.property.notifiable)}</small></button> })}</div> : <div className="mapping-context"><b>暂无消费端属性目录</b><p>目标适配器 {consumerId ?? '未指定'} 尚未发布该设备模型的属性，不能回退使用其他消费者的属性。</p></div> : <div className="mapping-context"><b>消费端边界（Consumer）</b><p>属性目录由具体目标适配器发布，统一模型层不预设 HomeKit、Matter 或其他协议字段。</p></div>}
      </section>
    </div>
    <div className={`mapping-route-toolbar ${editingID || editingDefaultKey ? 'is-editing' : ''}`}><label>转换配置（Profile）<select aria-label="映射转换 Profile" value={profileId} onChange={(event) => setProfileId(event.target.value)}><option value="">恒等转换（identity）· 不转换</option>{compatibleProfiles.map((item) => <option key={item.id} value={item.id}>{item.id} · {item.transforms.map((transform) => transform.type).join(' → ') || 'identity'}</option>)}</select></label><div><small>{editingDefaultKey ? '正在修改默认映射；保存后写入当前设备的独立覆盖。' : editingID ? `正在编辑数据库路由 ${editingID}` : inputType && outputType ? `类型：${valueTypeLabel(inputType)} → ${valueTypeLabel(outputType)}；同一来源可继续映射到其他模型属性。` : '请选择两端属性'}</small>{(editingID || editingDefaultKey) && <button onClick={clearEditing}>取消编辑</button>}<button className="add-button" disabled={saving || !modelParameter || (stage === 'provider' ? !source : !consumer || !consumerDevice) || (!profileId && inputType !== outputType) || enumProfileRequired} onClick={() => void save()}>{saving ? '保存中…' : editingDefaultKey ? '保存默认映射覆盖' : editingID ? '保存路由修改' : `＋ 保存第 ${stage === 'provider' ? '一' : '二'} 段路由`}</button></div>{enumCompatibility.kind !== 'none' && <div className={`enum-compatibility is-${profileId ? 'profile' : enumCompatibility.kind}`} role="status"><header><strong>枚举值域检查（ENUM DOMAIN）</strong><span>{profileId ? `由 Profile ${profileId} 转换` : enumCompatibility.kind === 'exact' ? '完全一致，可直接映射' : enumCompatibility.kind === 'normalized' ? '仅格式差异，可自动对齐' : enumCompatibility.kind === 'partial' ? '部分兼容，存在模型独有值' : '语义不一致，需要 Profile'}</span></header><div className="enum-domain-comparison"><section><small>来源值域（Provider）</small><div>{enumCompatibility.source.map((item) => <code key={item}>{item}</code>)}</div></section><i>→</i><section><small>统一模型值域（Model）</small><div>{enumCompatibility.target.map((item) => <code key={item}>{item}</code>)}</div></section></div><div className="enum-pair-list">{enumCompatibility.pairs.map((item) => <span key={`${item.source}/${item.target}`}><code>{item.source}</code> → <code>{item.target}</code></span>)}</div>{!profileId && enumCompatibility.targetOnly.length > 0 && <p>模型独有：<code>{enumCompatibility.targetOnly.join(' / ')}</code>；此设备不能反向写入这些值。</p>}{!profileId && enumCompatibility.sourceOnly.length > 0 && <p>无法自动对齐：<code>{enumCompatibility.sourceOnly.join(' / ')}</code>；请选择枚举转换 Profile 后保存。</p>}</div>}</div>
    <div className="mapping-route-list"><div className="command-heading"><h3>当前设备路由</h3><span>{visibleDefaultProviderRoutes.length > 0 ? `${visibleDefaultProviderRoutes.length} 条模型默认 · ` : ''}数据库覆盖 · {targetId && consumerDeviceId ? `${targetId} / ${consumerDeviceId}` : `${device.providerId} / ${device.id}`}</span></div>{stage === 'provider' && <p className="mapping-route-priority">优先级：手工数据库路由覆盖相同目标的模型默认路由；同一来源可以保留多条指向不同模型属性的路由。</p>}{visibleDefaultProviderRoutes.map((item) => <article key={`default-${item.key}`} className="is-default"><span className="route-stage is-default">模型默认（DEFAULT）</span><div><strong>{item.source.deviceName}</strong><code>{item.source.endpointId}.{item.source.capabilityId}.{item.source.propertyId} → {item.model.path.endpointId}.{item.model.path.capabilityId}.{item.model.path.propertyId}</code><small>{deviceTypeLabel(item.source.deviceType)} · 恒等转换（identity）· 目标未被手工路由占用时生效</small></div><div><button aria-label={`编辑默认映射 ${item.source.propertyId}`} onClick={() => editDefault(item)}>编辑覆盖</button></div></article>)}{bindings.map((item) => <article key={item.id} className={item.enabled ? '' : 'is-disabled'}><span className={`route-stage is-${item.stage}`}>{item.stage === 'provider' ? '数据库覆盖（P → M）' : '模型 →消费端（M → C）'}</span><div><strong>{item.targetId && item.consumerDeviceId ? `${item.targetId} / ${item.consumerDeviceId}` : `${item.providerId} / ${item.deviceId}`}</strong><code>{item.stage === 'provider' ? `${item.endpointId}.${item.capabilityId}.${item.propertyId}` : `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}`} → {item.stage === 'provider' ? `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}` : `${item.consumerId}.${item.consumerProperty}`}</code><small>{item.deviceType ? deviceTypeLabel(item.deviceType) : '设备类型未指定'} · {item.profileId || '恒等转换（identity）'} · {item.enabled ? '实时生效' : '已停用'}</small></div><div><button aria-label={`编辑映射路由 ${item.id}`} onClick={() => edit(item)}>编辑</button><button onClick={() => void toggle(item)}>{item.enabled ? '停用' : '启用'}</button><button className="danger-link" onClick={() => void remove(item)}>删除</button></div></article>)}{bindings.length === 0 && visibleDefaultProviderRoutes.length === 0 && <p className="mapping-route-empty">当前设备没有可自动匹配的默认路由，请从上方选择来源属性和统一模型属性后保存。</p>}</div>
  </section>
}
