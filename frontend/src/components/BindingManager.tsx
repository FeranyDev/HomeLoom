import { useCallback, useEffect, useMemo, useState } from 'react'
import * as mappingApi from '../api/mapping'
import type { Device, DeviceType, PropertyDefinition, PropertyValue } from '../types/device'
import type { ConsumerProperty, MappingBinding, MappingCatalog, MappingProfileInfo, SourceCatalogDevice, SourceCatalogMetadata, SourceValueStatus } from '../types/mapping'
import { consumerPropertyLabel, deviceTypeLabel, matterClusterLabel, matterConsumerPathLabel, parameterLevelLabel, permissionLabel, propertyDisplayLabel, valueTypeLabel } from '../presentationLabels'
import { openProfileWorkbench } from '../profileDraft'

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

type NumericDefinition = { min?: number; max?: number; step?: number; unit?: string }

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
const selectablePropertyCardStyle = { WebkitUserSelect: 'text', userSelect: 'text' } as const

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

type EnumDomain = { type?: string; enum?: string[] }

function compareEnumDomains(sourceDefinition?: EnumDomain, targetDefinition?: EnumDomain): EnumCompatibility {
  const source = sourceDefinition?.type === 'enum' ? sourceDefinition.enum ?? [] : []
  const target = targetDefinition?.type === 'enum' ? targetDefinition.enum ?? [] : []
  if (sourceDefinition?.type !== 'enum' || targetDefinition?.type !== 'enum' || source.length === 0 || target.length === 0) {
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
  return (devices ?? []).flatMap((item) => (item.endpoints ?? []).flatMap((endpoint) => (endpoint.capabilities ?? []).flatMap((capability) => (capability.properties ?? []).map((property) => ({
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

function numericRangeText(definition?: NumericDefinition): string {
  if (!definition || (definition.min === undefined && definition.max === undefined)) return '未声明'
  const minimum = definition.min === undefined ? '−∞' : String(definition.min)
  const maximum = definition.max === undefined ? '+∞' : String(definition.max)
  return `${minimum} ～ ${maximum}${definition.step !== undefined ? `，步长 ${definition.step}` : ''}${definition.unit ? ` ${definition.unit}` : ''}`
}

function unitValue(value: number, from?: string, to?: string): number | undefined {
  if (!from || !to || from === to) return value
  if (from === 'celsius' && to === 'fahrenheit') return value * 9 / 5 + 32
  if (from === 'fahrenheit' && to === 'celsius') return (value - 32) * 5 / 9
  if (from === 'ratio' && to === 'percent') return value * 100
  if (from === 'percent' && to === 'ratio') return value / 100
  if ((from === 'kelvin' && to === 'mired') || (from === 'mired' && to === 'kelvin')) return value > 0 ? 1_000_000 / value : undefined
  return undefined
}

function projectNumericDefinition(definition: NumericDefinition | undefined, profile?: MappingProfileInfo): NumericDefinition | undefined {
  if (!definition) return undefined
  const current: NumericDefinition = { ...definition }
  for (const transform of profile?.transforms ?? []) {
    const endpoints = [current.min, current.max].filter((value): value is number => value !== undefined)
    let values: number[] | undefined
    if (transform.type === 'int-number') continue
    if (transform.type === 'scale') {
      values = endpoints.map((value) => value * (transform.factor ?? 0) + (transform.offset ?? 0))
      if (current.step !== undefined) current.step = Math.abs(current.step * (transform.factor ?? 0))
    } else if (transform.type === 'reciprocal') {
      if (endpoints.some((value) => value === 0)) return undefined
      values = endpoints.map((value) => 1 / value); current.step = undefined
    } else if (transform.type === 'unit') {
      values = endpoints.map((value) => unitValue(value, transform.fromUnit, transform.toUnit)).filter((value): value is number => value !== undefined)
      if (values.length !== endpoints.length) return undefined
      if (current.step !== undefined && transform.fromUnit && transform.toUnit && !['kelvin', 'mired'].includes(transform.fromUnit) && !['kelvin', 'mired'].includes(transform.toUnit)) {
        const zero = unitValue(0, transform.fromUnit, transform.toUnit)
        const stepped = unitValue(current.step, transform.fromUnit, transform.toUnit)
        current.step = zero !== undefined && stepped !== undefined ? Math.abs(stepped - zero) : undefined
      } else current.step = undefined
      current.unit = transform.toUnit
    } else if (transform.type === 'map-range') {
      const span = (transform.inputMax ?? 0) - (transform.inputMin ?? 0)
      if (span === 0) return undefined
      values = endpoints.map((value) => (transform.outputMin ?? 0) + (value - (transform.inputMin ?? 0)) * ((transform.outputMax ?? 0) - (transform.outputMin ?? 0)) / span)
      current.step = current.step === undefined ? undefined : Math.abs(current.step * ((transform.outputMax ?? 0) - (transform.outputMin ?? 0)) / span)
    } else if (transform.type === 'round') {
      const round = transform.mode === 'floor' ? Math.floor : transform.mode === 'ceil' ? Math.ceil : Math.round
      values = endpoints.map(round); current.step = 1
    } else if (transform.type === 'clamp') {
      if (current.min === undefined || (transform.min !== undefined && transform.min > current.min)) current.min = transform.min
      if (current.max === undefined || (transform.max !== undefined && transform.max < current.max)) current.max = transform.max
      continue
    } else {
      return undefined
    }
    if (values.length > 0) {
      current.min = Math.min(...values)
      current.max = Math.max(...values)
    }
  }
  return current
}

function intersectNumericDefinitions(source?: NumericDefinition, target?: NumericDefinition): NumericDefinition | undefined {
  if (!source || !target || (source.unit && target.unit && source.unit !== target.unit)) return undefined
  const minimum = source.min === undefined ? target.min : target.min === undefined ? source.min : Math.max(source.min, target.min)
  const maximum = source.max === undefined ? target.max : target.max === undefined ? source.max : Math.min(source.max, target.max)
  if (minimum !== undefined && maximum !== undefined && minimum > maximum) return undefined
  const steps = [source.step, target.step].filter((value): value is number => value !== undefined)
  return { min: minimum, max: maximum, step: steps.length ? Math.max(...steps) : undefined, unit: target.unit ?? source.unit }
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
  const [modelPropertyFilter, setModelPropertyFilter] = useState<'all' | 'publisher-bound'>('all')
  const [lockedProviderSourceKey, setLockedProviderSourceKey] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [nextBindings, nextProfiles, nextCatalog] = await Promise.all([api.listBindings(), api.listProfiles(), api.catalog()])
      const deviceBindings = nextBindings.filter((item) => item.providerId === device.providerId && item.deviceId === device.id)
      const visibleBindings = deviceBindings.filter((item) => {
        if (consumerOnly) {
          // Keep provider-stage bindings so consumer mapping can mark model parameters
          // that already have a publisher source, while only editing consumer routes.
          if (item.stage === 'provider') return true
          return item.stage === 'consumer' && item.targetId === targetId && item.consumerDeviceId === consumerDeviceId && (!consumerId || item.consumerId === consumerId)
        }
        if (providerOnly) return item.stage === 'provider'
        return !item.targetId && !item.consumerDeviceId
      })
      setBindings(visibleBindings); setProfiles(nextProfiles); setCatalog(nextCatalog); setError('')
    } catch (cause) { setError(routeError(cause)) }
  }, [api, consumerDeviceId, consumerId, consumerOnly, device.id, device.providerId, providerOnly, targetId])
  useEffect(() => { void refresh() }, [profileRevision, catalogRevision, refresh])

  const catalogDevice = catalog.providers.find((item) => item.providerId === device.providerId && item.id === device.id) ?? fallbackCatalogDevice
  const catalogMetadata = catalogDevice.catalog ?? fallbackMetadata
  const sources = useMemo(() => providerProperties([catalogDevice], catalogMetadata), [catalogDevice, catalogMetadata])
  const sourceCommands = (catalogDevice.endpoints ?? []).flatMap((endpoint) => (endpoint.capabilities ?? []).flatMap((capability) => capability.commands?.map((command) => ({ endpoint, capability, command })) ?? []))
  const sourceEvents = (catalogDevice.endpoints ?? []).flatMap((endpoint) => (endpoint.capabilities ?? []).flatMap((capability) => capability.events?.map((event) => ({ endpoint, capability, event })) ?? []))
  const source = sources.find((item) => item.key === sourceKey)
  const consumerDevice = catalogDevice
  const effectiveType = stage === 'provider' ? source?.deviceType ?? device.type : consumerDevice.type
  const targetConsumerType = consumerDeviceType ?? consumerDevice.type
  const model = catalog.models.find((item) => item.deviceType === effectiveType)
  const parameters = useMemo(() => orderModelParameters(model?.parameters ?? []), [model])
  const modelParameter = parameters.find((item) => pathKey(item.path) === modelKey)
  const consumerCatalogs = consumerId ? (catalog.consumers ?? []).filter((item) => item.id === consumerId) : (catalog.consumers ?? [])
  const consumers = consumerCatalogs.flatMap((item) => (item.properties ?? []).map((property) => ({ consumer: item, property })))
    .filter((item) => item.property.deviceType === targetConsumerType)
    .sort((left, right) => Number(right.property.level === 'required') - Number(left.property.level === 'required'))
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
  const enumCompatibility = compareEnumDomains(stage === 'provider' ? source?.definition : modelParameter, stage === 'provider' ? modelParameter : consumer?.property)
  const enumProfileRequired = enumCompatibility.kind === 'requires-profile' && !profileId
  const enumSourceLabel = stage === 'provider' ? '来源值域（Provider）' : '统一模型值域（Model）'
  const enumTargetLabel = stage === 'provider' ? '统一模型值域（Model）' : '消费端值域（Consumer）'
  const enumTargetOnlyLabel = stage === 'provider' ? '模型独有' : '消费端独有'
  const enumTargetOnlyHint = stage === 'provider' ? '此设备不能反向写入这些值。' : '统一模型不能写入这些值。'
  const enumPartialLabel = stage === 'provider' ? '部分兼容，存在模型独有值' : '部分兼容，存在消费端独有值'

  const compatibleProfiles = profiles.filter((item) => item.inputType === inputType && item.outputType === outputType && !(item.transforms ?? []).some((transform) => transform.type === 'clamp') && (stage === 'provider' ? item.kind !== 'target' : item.kind !== 'provider'))
  const selectedProfile = profiles.find((item) => item.id === profileId)
  const numericSource = stage === 'provider' ? source?.definition : modelParameter
  const numericTarget = stage === 'provider' ? modelParameter : consumer?.property
  const projectedNumericSource = projectNumericDefinition(numericSource, selectedProfile)
  const effectiveNumericRange = intersectNumericDefinitions(projectedNumericSource, numericTarget)
  const showNumericRange = (inputType === 'int' || inputType === 'number') && (outputType === 'int' || outputType === 'number')
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
  const listedBindings = useMemo(() => {
    if (consumerOnly) return bindings.filter((item) => item.stage === 'consumer')
    if (providerOnly) return bindings.filter((item) => item.stage === 'provider')
    return bindings
  }, [bindings, consumerOnly, providerOnly])
  const publisherBoundModelKeys = useMemo(() => {
    const keys = new Set<string>()
    for (const item of bindings) {
      if (item.stage !== 'provider' || !item.enabled) continue
      keys.add(pathKey({ endpointId: item.modelEndpointId, capabilityId: item.modelCapabilityId, propertyId: item.modelPropertyId }))
    }
    for (const route of defaultProviderRoutes) {
      keys.add(pathKey(route.model.path))
    }
    return keys
  }, [bindings, defaultProviderRoutes])
  const visibleParameters = useMemo(() => (
    stage === 'consumer' && modelPropertyFilter === 'publisher-bound'
      ? parameters.filter((item) => publisherBoundModelKeys.has(pathKey(item.path)))
      : parameters
  ), [modelPropertyFilter, parameters, publisherBoundModelKeys, stage])
  const publisherBoundParameterCount = parameters.filter((item) => publisherBoundModelKeys.has(pathKey(item.path))).length
  const effectiveRouteCount = listedBindings.filter((item) => item.enabled).length + visibleDefaultProviderRoutes.length
  const displayedRouteCount = listedBindings.length + visibleDefaultProviderRoutes.length

  useEffect(() => {
    if (!sourceKey && sources[0]) setSourceKey(sources[0].key)
  }, [sourceKey, sources])
  useEffect(() => {
    if (!visibleParameters.some((item) => pathKey(item.path) === modelKey)) setModelKey(visibleParameters[0] ? pathKey(visibleParameters[0].path) : '')
  }, [effectiveType, modelKey, visibleParameters])
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
    const itemModelKey = pathKey({ endpointId: item.modelEndpointId, capabilityId: item.modelCapabilityId, propertyId: item.modelPropertyId })
    if (item.stage === 'consumer') {
      setConsumerKey(`${item.consumerId}/${item.consumerProperty}`)
      if (!publisherBoundModelKeys.has(itemModelKey)) setModelPropertyFilter('all')
    }
    setModelKey(itemModelKey)
    setProfileId(item.profileId ?? ''); setEditingID(item.id); setEditingDefaultKey(null); setEditingEnabled(item.enabled)
  }

  const toggle = async (item: MappingBinding) => { try { await api.update(item.id, { ...item, enabled: !item.enabled }); await refresh() } catch (cause) { setError(routeError(cause)) } }
  const remove = async (item: MappingBinding) => { if (!window.confirm(`删除映射路由 ${item.id}？`)) return; try { await api.remove(item.id); await refresh() } catch (cause) { setError(routeError(cause)) } }


  const openProfileForCurrentMismatch = () => {
    if (!inputType || !outputType) return
    const sourceEnum = stage === 'provider' ? source?.definition.enum : modelParameter?.enum
    const targetEnum = stage === 'provider' ? modelParameter?.enum : consumer?.property.enum
    const sourceLabel = stage === 'provider'
      ? `${source?.propertyId ?? 'source'}`
      : `${modelParameter?.path.propertyId ?? 'model'}`
    const targetLabel = stage === 'provider'
      ? `${modelParameter?.path.propertyId ?? 'model'}`
      : `${consumer?.property.id ?? 'consumer'}`
    openProfileWorkbench({
      stage,
      inputType,
      outputType,
      sourceEnum,
      targetEnum,
      sourceLabel,
      targetLabel,
      reason: enumCompatibility.kind !== 'none' && enumCompatibility.kind !== 'exact'
        ? enumCompatibility.kind
        : inputType !== outputType ? 'type-mismatch' : 'manual',
    })
  }

  const showProfileJump = Boolean(inputType && outputType && (
    enumProfileRequired
    || (!profileId && enumCompatibility.kind === 'partial')
    || (!profileId && inputType !== outputType)
  ))
  return <section className="binding-manager mapping-graph">
    <div className="profile-heading"><div><p className="eyebrow">设备映射（DEVICE MAPPING） · {device.providerId}</p><h3>{consumerLabel ?? (providerOnly ? `${device.name} · 来源属性映射` : `${device.name}的双段属性路由`)}</h3><p>{consumerOnly ? `从来源设备的统一模型属性绑定到当前目标设备的 ${consumerId ?? 'Consumer'} 属性；每条属性可独立选择转换配置（Profile）。` : providerOnly ? '将这台提供端（Provider）设备的完整来源属性绑定到统一模型；路径和类型一致的属性会显示为可编辑的默认映射，保存修改后成为当前设备独立的数据库覆盖。' : '本编辑器只读取和修改当前设备。提供端（Provider）与消费端（Consumer）通过统一模型通信，两段路由可分别转换、启停和热更新。'}</p></div><span>{effectiveRouteCount} / {displayedRouteCount} 生效</span></div>
    {!consumerOnly && !providerOnly && <div className="mapping-stage-tabs" role="tablist"><button className={stage === 'provider' ? 'is-active' : ''} onClick={() => setStage('provider')}>① 提供端（Provider）→ 统一模型</button><button className={stage === 'consumer' ? 'is-active' : ''} onClick={() => setStage('consumer')}>② 统一模型 → 消费端（Consumer）</button></div>}
    {error && <p className="inline-error" role="alert">{error}</p>}
    <div className={`mapping-lanes is-${stage}-stage`}>
      {stage === 'provider' && <><section className="mapping-lane"><header><span>提供端（PROVIDERS）</span><strong>{catalogMetadata.complete ? '来源完整属性' : '来源属性（不完整）'}</strong><small>{sources.length} 属性 · {sourceCommands.length} 动作（Action）· {sourceEvents.length} 事件（Event）</small><span className={`catalog-status ${catalogMetadata.complete ? 'is-complete' : 'is-incomplete'}`}>{catalogMetadata.complete ? `完整 · ${catalogMetadata.source}` : `不完整 · ${catalogMetadata.source}`}</span>{catalogMetadata.specType && <code>{catalogMetadata.specType}</code>}{catalogMetadata.error && <small className="catalog-error">{catalogMetadata.error}</small>}</header>
        <div className="mapping-node-list">{sources.map((item) => <button key={item.key} style={selectablePropertyCardStyle} className={item.key === sourceKey ? 'is-selected' : ''} disabled={Boolean(lockedProviderSourceKey && item.key !== lockedProviderSourceKey)} title={lockedProviderSourceKey && item.key !== lockedProviderSourceKey ? '编辑覆盖时来源属性保持不变；如需更换来源，请取消编辑后新建路由。' : undefined} onClick={() => setSourceKey(item.key)}><span>{item.deviceName}</span><strong>{propertyDisplayLabel(item.definition.name, item.propertyId)}</strong><em className={`source-current-value ${item.valueStatus.known && item.valueStatus.available ? 'is-current' : item.valueStatus.known ? 'is-stale' : 'is-unknown'}`}>{item.valueStatus.known ? `${item.valueStatus.available ? '当前' : '上次'} ${propertyValueText(item.value)}${item.definition.unit ? ` ${item.definition.unit}` : ''}` : '当前值未知'}</em><code>{item.providerId} / {item.endpointId}.{item.capabilityId}.{item.propertyId}</code><small>{valueTypeLabel(item.definition.type)}{item.definition.unit ? ` · 单位 ${item.definition.unit}` : ''} · {permissionLabel(item.definition.readable, item.definition.writable, item.definition.notifiable)}{item.valueStatus.observedAt ? ` · ${new Date(item.valueStatus.observedAt).toLocaleTimeString('zh-CN')}` : ''}</small>{item.definition.type === 'enum' && item.definition.enum?.length ? <small className="enum-domain-inline">来源枚举：{item.definition.enum.join(' / ')}</small> : null}{item.valueStatus.error && <small className="catalog-error">{item.valueStatus.error}</small>}</button>)}</div>{(sourceCommands.length > 0 || sourceEvents.length > 0) && <details className="source-definition-summary"><summary>查看全部动作（Action）/ 事件（Event）</summary>{sourceCommands.map(({ endpoint, capability, command }) => <div key={`${endpoint.id}/${capability.id}/${command.id}`}><b>动作（Action）· {command.name}</b><code>{endpoint.id}.{capability.id}.{command.id}</code><small>{command.parameters?.length ?? 0} 个输入参数</small></div>)}{sourceEvents.map(({ endpoint, capability, event }) => <div key={`${endpoint.id}/${capability.id}/${event.id}`}><b>事件（Event）· {event.name}</b><code>{endpoint.id}.{capability.id}.{event.id}</code><small>{valueTypeLabel(event.payload)}</small></div>)}</details>}
      </section>
      <div className="mapping-arrow"><span>→</span><small>{profileId || 'identity'}</small></div></>}
      <section className="mapping-lane is-model"><header><span>统一模型（UNIFIED MODEL）</span><strong>端点 / 能力 / 属性（Endpoint / Capability / Property）三级基准</strong><label>当前设备<input aria-label="当前映射设备" value={`${device.name} · ${device.providerId} / ${device.id}`} disabled /></label><div className="mapping-model-controls"><label>设备模型（deviceType）<select aria-label="统一设备模型" value={effectiveType} disabled>{catalog.models.map((item) => <option key={item.deviceType} value={item.deviceType}>{deviceTypeLabel(item.deviceType)}</option>)}</select></label>{stage === 'consumer' && <label className="mapping-model-filter"><span className="mapping-model-filter-heading"><span>发布者属性筛选</span><small className="mapping-model-filter-count">{visibleParameters.length} / {parameters.length} 个属性可见</small></span><select aria-label="发布者属性筛选" value={modelPropertyFilter} onChange={(event) => setModelPropertyFilter(event.target.value as 'all' | 'publisher-bound')}><option value="all">全部统一模型属性（{parameters.length}）</option><option value="publisher-bound">仅已绑定发布者（{publisherBoundParameterCount}）</option></select></label>}</div></header>
        <div className="mapping-node-list">{visibleParameters.map((item) => {
          const key = pathKey(item.path)
          const publisherBound = stage === 'consumer' && publisherBoundModelKeys.has(key)
          return <button key={key} style={selectablePropertyCardStyle} className={`${key === modelKey ? 'is-selected' : ''}${publisherBound ? ' is-publisher-bound' : ''}`.trim()} onClick={() => setModelKey(key)}><span className={`parameter-level is-${item.level}`}>{parameterLevelLabel(item.level)}</span>{publisherBound && <span className="publisher-bound-mark" title="该统一模型属性已有发布者绑定，可从提供端投影到此属性">已绑定发布者</span>}<strong>{propertyDisplayLabel(item.name, item.path.propertyId)}</strong><code>{item.path.endpointId} / {item.path.capabilityId} / {item.path.propertyId}</code><small>{valueTypeLabel(item.type)}{item.unit ? ` · 单位 ${item.unit}` : ''} · {permissionLabel(item.readable, item.writable, item.notifiable)}</small>{item.type === 'enum' && item.enum?.length ? <small className="enum-domain-inline">模型枚举：{item.enum.join(' / ')}</small> : null}</button>
        })}{stage === 'consumer' && modelPropertyFilter === 'publisher-bound' && visibleParameters.length === 0 && <p className="mapping-filter-empty">当前设备没有已绑定发布者的统一模型属性，请先配置第一段路由。</p>}</div>
      </section>
      {stage === 'consumer' && <>
      <div className="mapping-arrow"><span>→</span><small>{profileId || 'identity'}</small></div>
	      <section className="mapping-lane">
	        <header>
	          <span>消费端（CONSUMERS）</span>
	          <strong>{consumerCatalogs.map((item) => item.name).join(' / ') || consumerId || '目标完整属性'}</strong>
	          <small>{consumers.length} 个属性</small>
	        </header>
	        {stage === 'consumer' ? consumers.length > 0 ? <div className="mapping-node-list">
	          {matterConsumerGroups.map((group) => <section className="matter-cluster-group" key={group.cluster}>
	            <header><span>Cluster</span><strong>{matterClusterLabel(group.cluster)}</strong><small>Cluster → Attribute / Command</small></header>
	            {group.items.map((item) => {
	              const key = `${item.consumer.id}/${item.property.id}`
	              const member = matterMember(item.property)
	              return <button key={key} style={selectablePropertyCardStyle} className={`${key === consumerKey ? 'is-selected' : ''}${item.property.level === 'required' ? ' is-consumer-required' : ''}`.trim()} onClick={() => setConsumerKey(key)}>
	                <span>Matter · {member.kind === 'command' ? 'Command（命令）' : 'Attribute（属性）'}</span>
	                <span className={`parameter-level is-${item.property.level}`}>{parameterLevelLabel(item.property.level)}属性</span>
	                <strong>{matterConsumerPathLabel(member.cluster, member.member, member.kind)}</strong>
	                <code>{member.cluster} → {member.kind === 'command' ? 'Command' : 'Attribute'} → {member.member}</code>
	                <small>{valueTypeLabel(item.property.type)} · {permissionLabel(item.property.readable, item.property.writable, item.property.notifiable)}</small>
	                {item.property.type === 'enum' && item.property.enum?.length ? <small className="enum-domain-inline">消费端枚举：{item.property.enum.join(' / ')}</small> : null}
	              </button>
	            })}
	          </section>)}
	          {otherConsumers.map((item) => {
	            const key = `${item.consumer.id}/${item.property.id}`
	            return <button key={key} style={selectablePropertyCardStyle} className={`${key === consumerKey ? 'is-selected' : ''}${item.property.level === 'required' ? ' is-consumer-required' : ''}`.trim()} onClick={() => setConsumerKey(key)}>
	              <span>{item.consumer.name}（{item.consumer.id}）</span>
	              <span className={`parameter-level is-${item.property.level}`}>{parameterLevelLabel(item.property.level)}属性</span>
	              <strong>{consumerPropertyLabel(item.property.id)}</strong>
	              <code>{item.property.id}</code>
	              <small>{valueTypeLabel(item.property.type)} · {permissionLabel(item.property.readable, item.property.writable, item.property.notifiable)}</small>
	              {item.property.type === 'enum' && item.property.enum?.length ? <small className="enum-domain-inline">消费端枚举：{item.property.enum.join(' / ')}</small> : null}
	            </button>
	          })}
	        </div> : <div className="mapping-context"><b>暂无消费端属性目录</b><p>目标适配器 {consumerId ?? '未指定'} 尚未发布该设备模型的属性，不能回退使用其他消费者的属性。</p></div> : <div className="mapping-context"><b>消费端边界（Consumer）</b><p>属性目录由具体目标适配器发布，统一模型层不预设 HomeKit、Matter 或其他协议字段。</p></div>}
	      </section>
      </>}
    </div>
    <div className={`mapping-route-toolbar ${editingID || editingDefaultKey ? 'is-editing' : ''}`}>
      <div className="mapping-profile-field"><label>转换配置（Profile）<select aria-label="映射转换 Profile" value={profileId} onChange={(event) => setProfileId(event.target.value)}><option value="">恒等转换（identity）· 不转换</option>{compatibleProfiles.map((item) => <option key={item.id} value={item.id}>{item.id} · {(item.transforms ?? []).map((transform) => transform.type).join(' → ') || 'identity'}</option>)}</select></label><button type="button" className="mapping-profile-refresh" aria-label="刷新转换配置列表" title="重新加载可用 Profile" onClick={() => void refresh()} disabled={saving}>刷新</button></div>
      <div className="mapping-route-actions"><small>{editingDefaultKey ? '正在修改默认映射；保存后写入当前设备的独立覆盖。' : editingID ? `正在编辑数据库路由 ${editingID}` : inputType && outputType ? `类型：${valueTypeLabel(inputType)} → ${valueTypeLabel(outputType)}；同一来源可继续映射到其他模型属性。` : '请选择两端属性'}</small>{showProfileJump && enumCompatibility.kind === 'none' && <button type="button" onClick={openProfileForCurrentMismatch}>去配置转换 Profile</button>}{(editingID || editingDefaultKey) && <button onClick={clearEditing}>取消编辑</button>}<button className="add-button" disabled={saving || !modelParameter || (stage === 'provider' ? !source : !consumer || !consumerDevice) || (!profileId && inputType !== outputType) || enumProfileRequired} onClick={() => void save()}>{saving ? '保存中…' : editingDefaultKey ? '保存默认映射覆盖' : editingID ? '保存路由修改' : `＋ 保存第 ${stage === 'provider' ? '一' : '二'} 段路由`}</button></div>
      {showNumericRange && <div className="numeric-range-comparison" role="status"><header><strong>数值范围（NUMERIC RANGE）</strong><span>最终范围由两端约束取交集</span></header><section><small>{stage === 'provider' ? '来源范围（Provider）' : '统一模型范围（Model）'}</small><code>{numericRangeText(numericSource)}</code></section><i>→</i><section><small>转换后范围</small><code>{projectedNumericSource ? numericRangeText(projectedNumericSource) : '无法静态推导'}</code></section><i>∩</i><section><small>{stage === 'provider' ? '统一模型范围（Model）' : `${consumer?.consumer.name ?? '消费端'}范围（Consumer）`}</small><code>{numericRangeText(numericTarget)}</code></section><i>=</i><section className={effectiveNumericRange ? 'is-effective' : 'is-empty'}><small>最终有效范围</small><code>{effectiveNumericRange ? numericRangeText(effectiveNumericRange) : '无有效交集'}</code></section></div>}
      {enumCompatibility.kind !== 'none' && <div className={`enum-compatibility is-${profileId ? 'profile' : enumCompatibility.kind}`} role="status"><header><strong>枚举值域检查（ENUM DOMAIN）</strong><span>{profileId ? `由 Profile ${profileId} 转换` : enumCompatibility.kind === 'exact' ? '完全一致，可直接映射' : enumCompatibility.kind === 'normalized' ? '仅格式差异，可自动对齐' : enumCompatibility.kind === 'partial' ? enumPartialLabel : '语义不一致，需要 Profile'}</span></header><div className="enum-domain-comparison"><section><small>{enumSourceLabel}</small><div>{enumCompatibility.source.map((item) => <code key={item}>{item}</code>)}</div></section><i>→</i><section><small>{enumTargetLabel}</small><div>{enumCompatibility.target.map((item) => <code key={item}>{item}</code>)}</div></section></div><div className="enum-pair-list">{enumCompatibility.pairs.map((item) => <span key={`${item.source}/${item.target}`}><code>{item.source}</code> → <code>{item.target}</code></span>)}</div>{!profileId && enumCompatibility.targetOnly.length > 0 && <p>{enumTargetOnlyLabel}：<code>{enumCompatibility.targetOnly.join(' / ')}</code>；{enumTargetOnlyHint}</p>}{!profileId && enumCompatibility.sourceOnly.length > 0 && <p>无法自动对齐：<code>{enumCompatibility.sourceOnly.join(' / ')}</code>；请选择枚举转换 Profile 后保存。</p>}{showProfileJump && <div className="mapping-profile-jump"><button type="button" className="add-button" onClick={openProfileForCurrentMismatch}>去配置转换 Profile</button><small>将按当前两端类型与枚举值预填草稿，并在新标签打开转换配置页。</small></div>}</div>}
    </div>
    <div className="mapping-route-list"><div className="command-heading"><h3>当前设备路由</h3><span>{visibleDefaultProviderRoutes.length > 0 ? `${visibleDefaultProviderRoutes.length} 条模型默认 · ` : ''}数据库覆盖 · {targetId && consumerDeviceId ? `${targetId} / ${consumerDeviceId}` : `${device.providerId} / ${device.id}`}</span></div>{stage === 'provider' && <p className="mapping-route-priority">优先级：手工数据库路由覆盖相同目标的模型默认路由；同一来源可以保留多条指向不同模型属性的路由。</p>}{visibleDefaultProviderRoutes.map((item) => <article key={`default-${item.key}`} className="is-default"><span className="route-stage is-default">模型默认（DEFAULT）</span><div><strong>{item.source.deviceName}</strong><code>{item.source.endpointId}.{item.source.capabilityId}.{item.source.propertyId} → {item.model.path.endpointId}.{item.model.path.capabilityId}.{item.model.path.propertyId}</code><small>{deviceTypeLabel(item.source.deviceType)} · 恒等转换（identity）· 目标未被手工路由占用时生效</small></div><div><button aria-label={`编辑默认映射 ${item.source.propertyId}`} onClick={() => editDefault(item)}>编辑覆盖</button></div></article>)}{listedBindings.map((item) => <article key={item.id} className={item.enabled ? '' : 'is-disabled'}><span className={`route-stage is-${item.stage}`}>{item.stage === 'provider' ? '数据库覆盖（P → M）' : '模型 →消费端（M → C）'}</span><div><strong>{item.targetId && item.consumerDeviceId ? `${item.targetId} / ${item.consumerDeviceId}` : `${item.providerId} / ${item.deviceId}`}</strong><code>{item.stage === 'provider' ? `${item.endpointId}.${item.capabilityId}.${item.propertyId}` : `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}`} → {item.stage === 'provider' ? `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}` : `${item.consumerId}.${item.consumerProperty}`}</code><small>{item.deviceType ? deviceTypeLabel(item.deviceType) : '设备类型未指定'} · {item.profileId || '恒等转换（identity）'} · {item.enabled ? '实时生效' : '已停用'}</small></div><div><button aria-label={`编辑映射路由 ${item.id}`} onClick={() => edit(item)}>编辑</button><button onClick={() => void toggle(item)}>{item.enabled ? '停用' : '启用'}</button><button className="danger-link" onClick={() => void remove(item)}>删除</button></div></article>)}{listedBindings.length === 0 && visibleDefaultProviderRoutes.length === 0 && <p className="mapping-route-empty">当前设备没有可自动匹配的默认路由，请从上方选择来源属性和统一模型属性后保存。</p>}</div>
  </section>
}
