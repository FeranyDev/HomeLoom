import { useCallback, useEffect, useMemo, useState } from 'react'
import * as mappingApi from '../api/mapping'
import type { Device, DeviceType, PropertyDefinition } from '../types/device'
import type { MappingBinding, MappingCatalog, MappingProfileInfo, SourceCatalogDevice, SourceCatalogMetadata } from '../types/mapping'

type ProviderProperty = {
  key: string; providerId: string; deviceId: string; deviceName: string; deviceType: DeviceType
  endpointId: string; capabilityId: string; propertyId: string; definition: PropertyDefinition
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

function providerProperties(devices: Device[]): ProviderProperty[] {
  return devices.flatMap((item) => item.endpoints.flatMap((endpoint) => endpoint.capabilities.flatMap((capability) => capability.properties.map((property) => ({
    key: `${item.providerId}/${item.id}/${endpoint.id}/${capability.id}/${property.definition.id}`,
    providerId: item.providerId, deviceId: item.id, deviceName: item.name, deviceType: item.type,
    endpointId: endpoint.id, capabilityId: capability.id, propertyId: property.definition.id, definition: property.definition,
  })))))
}

function permissionText(item: { readable: boolean; writable: boolean; notifiable: boolean }) {
  return `${item.readable ? 'R' : '–'}${item.writable ? 'W' : '–'}${item.notifiable ? 'N' : '–'}`
}

export function BindingManager({ device, profileRevision = 0, catalogRevision = 0, api = defaultAPI }: {
  device: Device; profileRevision?: number; catalogRevision?: number; api?: BindingAPI
}) {
  const [stage, setStage] = useState<'provider' | 'consumer'>('provider')
  const fallbackMetadata: SourceCatalogMetadata = { complete: false, source: 'device-snapshot', error: '原始属性目录尚未加载' }
  const fallbackCatalogDevice: SourceCatalogDevice = { ...device, catalog: fallbackMetadata }
  const [catalog, setCatalog] = useState<MappingCatalog>({ providers: [fallbackCatalogDevice], models: [], consumers: [] })
  const [bindings, setBindings] = useState<MappingBinding[]>([])
  const [profiles, setProfiles] = useState<MappingProfileInfo[]>([])
  const [sourceKey, setSourceKey] = useState('')
  const [modelKey, setModelKey] = useState('')
  const [consumerKey, setConsumerKey] = useState('')
  const [profileId, setProfileId] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [nextBindings, nextProfiles, nextCatalog] = await Promise.all([api.listBindings(), api.listProfiles(), api.catalog()])
      setBindings(nextBindings.filter((item) => item.providerId === device.providerId && item.deviceId === device.id)); setProfiles(nextProfiles); setCatalog(nextCatalog); setError('')
    } catch (cause) { setError(routeError(cause)) }
  }, [api, device.id, device.providerId])
  useEffect(() => { void refresh() }, [profileRevision, catalogRevision, refresh])

  const catalogDevice = catalog.providers.find((item) => item.providerId === device.providerId && item.id === device.id) ?? fallbackCatalogDevice
  const catalogMetadata = catalogDevice.catalog ?? fallbackMetadata
  const sources = useMemo(() => providerProperties([catalogDevice]), [catalogDevice])
  const sourceCommands = catalogDevice.endpoints.flatMap((endpoint) => endpoint.capabilities.flatMap((capability) => capability.commands?.map((command) => ({ endpoint, capability, command })) ?? []))
  const sourceEvents = catalogDevice.endpoints.flatMap((endpoint) => endpoint.capabilities.flatMap((capability) => capability.events?.map((event) => ({ endpoint, capability, event })) ?? []))
  const source = sources.find((item) => item.key === sourceKey)
  const consumerDevice = catalogDevice
  const effectiveType = stage === 'provider' ? source?.deviceType ?? device.type : consumerDevice.type
  const model = catalog.models.find((item) => item.deviceType === effectiveType)
  const parameters = useMemo(() => model?.parameters ?? [], [model])
  const modelParameter = parameters.find((item) => pathKey(item.path) === modelKey)
  const consumers = catalog.consumers.flatMap((item) => item.properties.map((property) => ({ consumer: item, property }))).filter((item) => item.property.deviceType === effectiveType)
  const consumer = consumers.find((item) => `${item.consumer.id}/${item.property.id}` === consumerKey)
  const inputType = stage === 'provider' ? source?.definition.type : modelParameter?.type
  const outputType = stage === 'provider' ? modelParameter?.type : consumer?.property.type
  const compatibleProfiles = profiles.filter((item) => item.inputType === inputType && item.outputType === outputType && !item.transforms.some((transform) => transform.type === 'clamp') && (stage === 'provider' ? item.kind !== 'target' : item.kind !== 'provider'))

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

  const create = async () => {
    if (!modelParameter || (stage === 'provider' ? !source : !consumer)) return
    setSaving(true); setError('')
    try {
      const common = { stage, profileId: profileId || undefined, modelEndpointId: modelParameter.path.endpointId, modelCapabilityId: modelParameter.path.capabilityId, modelPropertyId: modelParameter.path.propertyId, enabled: true }
      if (stage === 'provider' && source) {
        await api.create({ ...common, deviceType: source.deviceType, providerId: source.providerId, deviceId: source.deviceId, endpointId: source.endpointId, capabilityId: source.capabilityId, propertyId: source.propertyId })
      } else if (consumer && consumerDevice) {
        await api.create({ ...common, providerId: consumerDevice.providerId, deviceId: consumerDevice.id, deviceType: consumerDevice.type, consumerId: consumer.consumer.id, consumerProperty: consumer.property.id })
      }
      await refresh()
    } catch (cause) { setError(routeError(cause)) } finally { setSaving(false) }
  }

  const toggle = async (item: MappingBinding) => { try { await api.update(item.id, { ...item, enabled: !item.enabled }); await refresh() } catch (cause) { setError(routeError(cause)) } }
  const remove = async (item: MappingBinding) => { if (!window.confirm(`删除映射路由 ${item.id}？`)) return; try { await api.remove(item.id); await refresh() } catch (cause) { setError(routeError(cause)) } }

  return <section className="binding-manager mapping-graph">
    <div className="profile-heading"><div><p className="eyebrow">DEVICE MAPPING · {device.providerId}</p><h3>{device.name}的双段属性路由</h3><p>本编辑器只读取和修改当前设备。Provider 与 Consumer 通过统一模型通信，两段路由可分别转换、启停和热更新。</p></div><span>{bindings.filter((item) => item.enabled).length} / {bindings.length} 生效</span></div>
    <div className="mapping-stage-tabs" role="tablist"><button className={stage === 'provider' ? 'is-active' : ''} onClick={() => setStage('provider')}>① Provider → 统一模型</button><button className={stage === 'consumer' ? 'is-active' : ''} onClick={() => setStage('consumer')}>② 统一模型 → Consumer</button></div>
    {error && <p className="inline-error" role="alert">{error}</p>}
    <div className="mapping-lanes">
      <section className={`mapping-lane ${stage === 'provider' ? '' : 'is-context'}`}><header><span>PROVIDERS</span><strong>{catalogMetadata.complete ? '来源完整属性' : '来源属性（不完整）'}</strong><small>{sources.length} 属性 · {sourceCommands.length} Action · {sourceEvents.length} Event</small><span className={`catalog-status ${catalogMetadata.complete ? 'is-complete' : 'is-incomplete'}`}>{catalogMetadata.complete ? `完整 · ${catalogMetadata.source}` : `不完整 · ${catalogMetadata.source}`}</span>{catalogMetadata.specType && <code>{catalogMetadata.specType}</code>}{catalogMetadata.error && <small className="catalog-error">{catalogMetadata.error}</small>}</header>
        {stage === 'provider' ? <><div className="mapping-node-list">{sources.map((item) => <button key={item.key} className={item.key === sourceKey ? 'is-selected' : ''} onClick={() => setSourceKey(item.key)}><span>{item.deviceName}</span><strong>{item.definition.name}</strong><code>{item.providerId} / {item.endpointId}.{item.capabilityId}.{item.propertyId}</code><small>{item.definition.type}{item.definition.unit ? ` · ${item.definition.unit}` : ''} · {permissionText(item.definition)}</small></button>)}</div>{(sourceCommands.length > 0 || sourceEvents.length > 0) && <details className="source-definition-summary"><summary>查看全部 Action / Event</summary>{sourceCommands.map(({ endpoint, capability, command }) => <div key={`${endpoint.id}/${capability.id}/${command.id}`}><b>Action · {command.name}</b><code>{endpoint.id}.{capability.id}.{command.id}</code><small>{command.parameters?.length ?? 0} 个输入参数</small></div>)}{sourceEvents.map(({ endpoint, capability, event }) => <div key={`${endpoint.id}/${capability.id}/${event.id}`}><b>Event · {event.name}</b><code>{endpoint.id}.{capability.id}.{event.id}</code><small>{event.payload}</small></div>)}</details>}</> : <div className="mapping-context"><b>Provider 边界</b><p>Consumer 不直接读取 Provider 字段，避免平台之间形成隐式耦合。</p></div>}
      </section>
      <div className="mapping-arrow"><span>→</span><small>{stage === 'provider' ? profileId || 'identity' : '统一语义'}</small></div>
      <section className="mapping-lane is-model"><header><span>UNIFIED MODEL</span><strong>三级属性基准</strong><label>当前设备<input aria-label="当前映射设备" value={`${device.name} · ${device.providerId} / ${device.id}`} disabled /></label><label>设备模型<select aria-label="统一设备模型" value={effectiveType} disabled>{catalog.models.map((item) => <option key={item.deviceType}>{item.deviceType}</option>)}</select></label></header>
        <div className="mapping-node-list">{parameters.map((item) => <button key={pathKey(item.path)} className={pathKey(item.path) === modelKey ? 'is-selected' : ''} onClick={() => setModelKey(pathKey(item.path))}><span className={`parameter-level is-${item.level}`}>{item.level}</span><strong>{item.name}</strong><code>{item.path.endpointId} / {item.path.capabilityId} / {item.path.propertyId}</code><small>{item.type}{item.unit ? ` · ${item.unit}` : ''} · {permissionText(item)}</small></button>)}</div>
      </section>
      <div className="mapping-arrow"><span>→</span><small>{stage === 'consumer' ? profileId || 'identity' : '统一状态'}</small></div>
      <section className={`mapping-lane ${stage === 'consumer' ? '' : 'is-context'}`}><header><span>CONSUMERS</span><strong>目标完整属性</strong><small>{consumers.length} 个属性</small></header>
        {stage === 'consumer' ? <div className="mapping-node-list">{consumers.map((item) => { const key = `${item.consumer.id}/${item.property.id}`; return <button key={key} className={key === consumerKey ? 'is-selected' : ''} onClick={() => setConsumerKey(key)}><span>{item.consumer.name}</span><strong>{item.property.name}</strong><code>{item.property.id}</code><small>{item.property.type} · {item.property.level} · {permissionText(item.property)}</small></button> })}</div> : <div className="mapping-context"><b>Consumer 边界</b><p>HomeKit 当前展示全部可提供的 Service / Characteristic 属性。</p></div>}
      </section>
    </div>
    <div className="mapping-route-toolbar"><label>转换 Profile<select aria-label="映射转换 Profile" value={profileId} onChange={(event) => setProfileId(event.target.value)}><option value="">Identity · 不转换</option>{compatibleProfiles.map((item) => <option key={item.id} value={item.id}>{item.id} · {item.transforms.map((transform) => transform.type).join(' → ') || 'identity'}</option>)}</select></label><div><small>{inputType && outputType ? `类型：${inputType} → ${outputType}` : '请选择两端属性'}</small><button className="add-button" disabled={saving || !modelParameter || (stage === 'provider' ? !source : !consumer || !consumerDevice) || (!profileId && inputType !== outputType)} onClick={() => void create()}>{saving ? '保存中…' : `＋ 保存第 ${stage === 'provider' ? '一' : '二'} 段路由`}</button></div></div>
    <div className="mapping-route-list"><div className="command-heading"><h3>当前设备路由</h3><span>数据库 · {device.providerId} / {device.id}</span></div>{bindings.map((item) => <article key={item.id} className={item.enabled ? '' : 'is-disabled'}><span className={`route-stage is-${item.stage}`}>{item.stage === 'provider' ? 'P → M' : 'M → C'}</span><div><strong>{item.providerId} / {item.deviceId}</strong><code>{item.stage === 'provider' ? `${item.endpointId}.${item.capabilityId}.${item.propertyId}` : `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}`} → {item.stage === 'provider' ? `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}` : `${item.consumerId}.${item.consumerProperty}`}</code><small>{item.deviceType} · {item.profileId || 'identity'} · {item.enabled ? '实时生效' : '已停用'}</small></div><div><button onClick={() => void toggle(item)}>{item.enabled ? '停用' : '启用'}</button><button className="danger-link" onClick={() => void remove(item)}>删除</button></div></article>)}{bindings.length === 0 && <p className="mapping-route-empty">当前设备还没有自定义路由，将继续使用模型默认映射。</p>}</div>
  </section>
}
