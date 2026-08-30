import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import * as mappingApi from '../api/mapping'
import type { Device, DeviceType, PropertyDefinition, PropertyValue } from '../types/device'
import type { ConsumerProperty, MappingBinding, MappingCatalog, MappingProfileInfo, SourceCatalogDevice, SourceCatalogMetadata, SourceValueStatus } from '../types/mapping'
import { consumerPropertyLabel, deviceTypeLabel, matterClusterLabel, matterConsumerPathLabel, parameterLevelLabel, permissionLabel, propertyDisplayLabel, valueTypeLabel } from '../presentationLabels'
import { openProfileWorkbench } from '../profileDraft'
import { HelpTooltip } from './HelpTooltip'

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
	preview?: typeof mappingApi.previewMapping
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
const minimumPresentationStep = 0.01

function roundedNumber(value: number): number {
  return Number(value.toPrecision(12))
}

function minimumCompatiblePresentationStep(sourceStep?: number): number {
  if (!Number.isFinite(sourceStep) || (sourceStep ?? 0) <= 0) return minimumPresentationStep
  return roundedNumber(Math.ceil(minimumPresentationStep / sourceStep! - 1e-9) * sourceStep!)
}

function isPresentationStep(value: number, sourceStep?: number): boolean {
  if (!Number.isFinite(value) || value < minimumPresentationStep) return false
  if (!Number.isFinite(sourceStep) || (sourceStep ?? 0) <= 0) return true
  const multiple = value / sourceStep!
  return Math.abs(multiple - Math.round(multiple)) <= 1e-9 * Math.max(1, Math.abs(multiple))
}

function normalizePresentationStep(value: number, sourceStep?: number): number {
  if (!Number.isFinite(sourceStep) || (sourceStep ?? 0) <= 0) return Math.max(minimumPresentationStep, roundedNumber(value))
  const minimumMultiple = Math.ceil(minimumPresentationStep / sourceStep! - 1e-9)
  return roundedNumber(Math.max(minimumMultiple, Math.round(value / sourceStep!)) * sourceStep!)
}

function parseReadbackDelays(value: string): number[] | null {
  const seconds = value.split(/[,，\s]+/).filter(Boolean).map(Number)
  if (seconds.length === 0 || seconds.length > 8 || seconds.some((delay) => !Number.isFinite(delay) || delay < 0.1 || delay > 300)) return null
  const milliseconds = seconds.map((delay) => Math.round(delay * 1000))
  if (milliseconds.some((delay, index) => index > 0 && delay <= milliseconds[index - 1])) return null
  return milliseconds
}

const readbackDelayText = (delays?: number[]) => (delays?.length ? delays : [500, 2000, 5000, 10000]).map((delay) => delay / 1000).join(', ')

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

type EnumProfileTransform = {
  index: number
  forward: Array<[source: string, target: string]>
  reverse: Array<[target: string, source: string]>
}

function enumProfileTransforms(profile?: MappingProfileInfo): EnumProfileTransform[] {
  return (profile?.transforms ?? []).flatMap((transform, index) => {
    if (transform.type !== 'enum') return []
    const sortPairs = (pairs: Array<[string, string]>) => pairs.sort(([leftKey, leftValue], [rightKey, rightValue]) => leftKey.localeCompare(rightKey) || leftValue.localeCompare(rightValue))
    return [{
      index,
      forward: sortPairs(Object.entries(transform.values ?? {})),
      reverse: sortPairs(Object.entries(transform.reverseValues ?? {})),
    }]
  })
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

function withPresentationStep(definition: NumericDefinition | undefined, presentationStep?: number): NumericDefinition | undefined {
  if (!definition || presentationStep === undefined) return definition
  return { ...definition, step: presentationStep }
}

function automaticCapabilityProfileIdentifier(source?: { type?: string; unit?: string }, target?: { type?: string; unit?: string }): string | undefined {
  const numeric = (type?: string) => type === 'int' || type === 'number'
  if (!numeric(source?.type) || !numeric(target?.type) || !source?.unit || !target?.unit || source.unit === target.unit) return undefined
  const supported = new Set(['celsius:fahrenheit', 'fahrenheit:celsius', 'celsius:kelvin', 'kelvin:celsius', 'kelvin:mired', 'mired:kelvin', 'ratio:percent', 'percent:ratio'])
  if (!supported.has(`${source.unit}:${target.unit}`)) return undefined
  return `builtin-capability-${source.unit}-to-${target.unit}-${source.type}-to-${target.type}`
}

function modelPropertyValue(item: Device, path?: { endpointId: string; capabilityId: string; propertyId: string }): PropertyValue | undefined {
  if (!path) return undefined
  return (item.endpoints ?? []).find((endpoint) => endpoint.id === path.endpointId)
    ?.capabilities.find((capability) => capability.id === path.capabilityId)
    ?.properties.find((property) => property.definition.id === path.propertyId)?.value
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
  const deletedBindingIDs = useRef(new Set<string>())
  const [recentlyDeletedRoute, setRecentlyDeletedRoute] = useState<MappingBinding | null>(null)
  const [profiles, setProfiles] = useState<MappingProfileInfo[]>([])
  const [sourceKey, setSourceKey] = useState('')
  const [modelKey, setModelKey] = useState('')
  const [consumerKey, setConsumerKey] = useState('')
  const [profileId, setProfileId] = useState('')
	const [automaticProfileID, setAutomaticProfileID] = useState<string | null>(null)
	const [hapPreviewValue, setHapPreviewValue] = useState<PropertyValue | null>(null)
	const [hapPreviewError, setHapPreviewError] = useState('')
	const [hapPreviewing, setHapPreviewing] = useState(false)
  const [editingID, setEditingID] = useState<string | null>(null)
  const [editingDefaultKey, setEditingDefaultKey] = useState<string | null>(null)
  const [editingEnabled, setEditingEnabled] = useState(true)
  const [readbackEnabled, setReadbackEnabled] = useState(false)
  const [readbackDelays, setReadbackDelays] = useState('0.5, 2, 5, 10')
  const [presentationStepEnabled, setPresentationStepEnabled] = useState(false)
  const [presentationStep, setPresentationStep] = useState('')
  const [modelPropertyFilter, setModelPropertyFilter] = useState<'all' | 'publisher-bound'>('all')
  const [lockedProviderSourceKey, setLockedProviderSourceKey] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [nextBindings, nextProfiles, nextCatalog] = await Promise.all([api.listBindings(), api.listProfiles(), api.catalog()])
      const deviceBindings = nextBindings
        .filter((item) => !deletedBindingIDs.current.has(item.id))
        .filter((item) => item.providerId === device.providerId && item.deviceId === device.id)
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
  const profileLabel = (id?: string) => {
    if (!id) return '恒等转换（identity）'
    const profile = profiles.find((item) => item.id === id)
    return profile?.identifier ?? profile?.id ?? id
  }
  const selectedEnumTransforms = enumProfileTransforms(selectedProfile)
  const numericTarget = stage === 'provider' ? modelParameter : consumer?.property
	const providerPresentationBase = stage === 'provider' ? projectNumericDefinition(source?.definition, selectedProfile) : undefined
	const presentationSourceStep = providerPresentationBase?.step
	const presentationMinimumStep = minimumCompatiblePresentationStep(presentationSourceStep)
	const persistedPresentationStep = (() => {
		if (!modelParameter) return undefined
		const binding = bindings.find((item) => {
			if (item.stage !== 'provider' || !item.enabled || item.modelEndpointId !== modelParameter.path.endpointId || item.modelCapabilityId !== modelParameter.path.capabilityId || item.modelPropertyId !== modelParameter.path.propertyId) return false
			return stage !== 'provider' || !source || (item.providerId === source.providerId && item.deviceId === source.deviceId && item.endpointId === source.endpointId && item.capabilityId === source.capabilityId && item.propertyId === source.propertyId)
		})
		return binding?.presentationStep != null && isPresentationStep(binding.presentationStep, presentationSourceStep) ? binding.presentationStep : undefined
	})()
	const parsedPresentationStep = Number(presentationStep)
	const editedPresentationStep = stage === 'provider' && presentationStepEnabled && isPresentationStep(parsedPresentationStep, presentationSourceStep) ? parsedPresentationStep : undefined
	const activePresentationStep = stage === 'provider'
		? presentationStepEnabled ? editedPresentationStep : editingID || editingDefaultKey ? undefined : persistedPresentationStep
		: persistedPresentationStep
	const numericSource = withPresentationStep(stage === 'provider' ? source?.definition : modelParameter, stage === 'consumer' ? activePresentationStep : undefined)
	const suggestedCapabilityProfileIdentifier = automaticCapabilityProfileIdentifier(numericSource, numericTarget)
	const suggestedCapabilityProfileID = suggestedCapabilityProfileIdentifier
		? compatibleProfiles.find((item) => item.identifier === suggestedCapabilityProfileIdentifier)?.id
		: undefined
	const hapPreviewInput = stage === 'consumer' && consumer?.consumer.id === 'homekit' ? modelPropertyValue(device, modelParameter?.path) : undefined
	const canPreviewHAPValue = Boolean(stage === 'consumer' && consumer?.consumer.id === 'homekit' && hapPreviewInput && (!consumer.property.kind || consumer.property.kind === 'attribute'))
  const projectedNumericSource = withPresentationStep(projectNumericDefinition(numericSource, selectedProfile), stage === 'provider' ? activePresentationStep : undefined)
	const routeNumericTarget = withPresentationStep(numericTarget, stage === 'provider' ? activePresentationStep : projectedNumericSource?.step)
  const effectiveNumericRange = intersectNumericDefinitions(projectedNumericSource, routeNumericTarget)
  const showNumericRange = (inputType === 'int' || inputType === 'number') && (outputType === 'int' || outputType === 'number')
  const sourceHasStep = stage === 'provider' && (source?.definition.type === 'int' || source?.definition.type === 'number') && (modelParameter?.type === 'int' || modelParameter?.type === 'number') && Number.isFinite(presentationSourceStep) && (presentationSourceStep ?? 0) > 0
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
	useEffect(() => {
		if (editingID || editingDefaultKey || !suggestedCapabilityProfileID || profileId || !compatibleProfiles.some((item) => item.id === suggestedCapabilityProfileID)) return
		setProfileId(suggestedCapabilityProfileID)
		setAutomaticProfileID(suggestedCapabilityProfileID)
	}, [compatibleProfiles, editingDefaultKey, editingID, profileId, suggestedCapabilityProfileID])
	useEffect(() => {
		if (automaticProfileID && automaticProfileID === profileId && automaticProfileID !== suggestedCapabilityProfileID) {
			setProfileId('')
			setAutomaticProfileID(null)
		}
	}, [automaticProfileID, profileId, suggestedCapabilityProfileID])
	useEffect(() => { setHapPreviewValue(null); setHapPreviewError('') }, [consumerKey, modelKey, profileId, stage])
  useEffect(() => {
    if (!recentlyDeletedRoute) return
    const timer = window.setTimeout(() => setRecentlyDeletedRoute(null), 8_000)
    return () => window.clearTimeout(timer)
  }, [recentlyDeletedRoute])

  const clearEditing = () => { setEditingID(null); setEditingDefaultKey(null); setEditingEnabled(true); setReadbackEnabled(false); setReadbackDelays('0.5, 2, 5, 10'); setPresentationStepEnabled(false); setPresentationStep(''); setLockedProviderSourceKey(null) }

	const previewHAPValue = async () => {
		if (!hapPreviewInput) return
		setHapPreviewing(true); setHapPreviewError('')
		try {
			if (!profileId) {
				setHapPreviewValue(hapPreviewInput)
				return
			}
			const result = await (api.preview ?? mappingApi.previewMapping)({ profileId, direction: 'forward', value: hapPreviewInput })
			setHapPreviewValue(result.value)
		} catch (cause) {
			setHapPreviewValue(null)
			setHapPreviewError(routeError(cause))
		} finally {
			setHapPreviewing(false)
		}
	}

  const save = async () => {
    if (!modelParameter || (stage === 'provider' ? !source : !consumer)) return
    const parsedReadbacks = stage === 'provider' && readbackEnabled ? parseReadbackDelays(readbackDelays) : []
    if (stage === 'provider' && readbackEnabled && parsedReadbacks === null) { setError('写后回读时点请输入递增的 0.1–300 秒，最多 8 个'); return }
    const savedPresentationStep = stage === 'provider' && presentationStepEnabled ? Number(presentationStep) : undefined
    if (stage === 'provider' && presentationStepEnabled && (!sourceHasStep || !isPresentationStep(savedPresentationStep ?? Number.NaN, presentationSourceStep))) { setError(`自定义步长必须是不小于 ${presentationMinimumStep} 且为来源步长 ${presentationSourceStep} 的整数倍。`); return }
    setSaving(true); setError('')
    try {
      const common = { stage, profileId: profileId || undefined, modelEndpointId: modelParameter.path.endpointId, modelCapabilityId: modelParameter.path.capabilityId, modelPropertyId: modelParameter.path.propertyId, enabled: editingID ? editingEnabled : true, ...(stage === 'provider' ? { readbackEnabled, readbackDelaysMs: parsedReadbacks ?? [], presentationStep: savedPresentationStep ?? null } : {}) }
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
		setAutomaticProfileID(null)
		setEditingID(null); setEditingDefaultKey(item.key); setEditingEnabled(true); setReadbackEnabled(false); setReadbackDelays('0.5, 2, 5, 10'); setPresentationStepEnabled(false); setPresentationStep(String(normalizePresentationStep(item.source.definition.step ?? minimumPresentationStep, item.source.definition.step))); setLockedProviderSourceKey(item.source.key)
  }

  const edit = (item: MappingBinding) => {
		const itemSource = item.stage === 'provider' ? sources.find((candidate) => candidate.providerId === item.providerId && candidate.deviceId === item.deviceId && candidate.endpointId === item.endpointId && candidate.capabilityId === item.capabilityId && candidate.propertyId === item.propertyId) : undefined
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
		setProfileId(item.profileId ?? ''); setAutomaticProfileID(null); setEditingID(item.id); setEditingDefaultKey(null); setEditingEnabled(item.enabled); setReadbackEnabled(Boolean(item.readbackEnabled)); setReadbackDelays(readbackDelayText(item.readbackDelaysMs)); setPresentationStepEnabled(item.presentationStep != null); setPresentationStep(item.presentationStep == null ? '' : String(normalizePresentationStep(item.presentationStep, itemSource?.definition.step)))
  }

  const toggle = async (item: MappingBinding) => { try { await api.update(item.id, { ...item, enabled: !item.enabled }); await refresh() } catch (cause) { setError(routeError(cause)) } }
  const remove = async (item: MappingBinding) => {
    if (!window.confirm(`删除映射路由 ${item.id}？`)) return
    try {
      await api.remove(item.id)
      deletedBindingIDs.current.add(item.id)
      setBindings((current) => current.filter((currentItem) => currentItem.id !== item.id))
      setRecentlyDeletedRoute(item)
      if (editingID === item.id) clearEditing()
      await refresh()
    } catch (cause) { setError(routeError(cause)) }
  }


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
  const renderSourceProperty = (item: ProviderProperty) => <button key={item.key} style={selectablePropertyCardStyle} className={item.key === sourceKey ? 'is-selected' : ''} disabled={Boolean(lockedProviderSourceKey && item.key !== lockedProviderSourceKey)} onClick={() => setSourceKey(item.key)}><span>{item.deviceName}</span><strong>{item.definition.name || propertyDisplayLabel('', item.propertyId).split('（')[0]}</strong><em className={`source-current-value ${item.valueStatus.known && item.valueStatus.available ? 'is-current' : item.valueStatus.known ? 'is-stale' : 'is-unknown'}`}>{item.valueStatus.known ? `${item.valueStatus.available ? '当前' : '上次'} ${propertyValueText(item.value)}${item.definition.unit ? ` ${item.definition.unit}` : ''}` : '当前值未知'}</em><code>{item.providerId} / {item.endpointId}.{item.capabilityId}.{item.propertyId}</code><small>{valueTypeLabel(item.definition.type)}{item.definition.unit ? ` · 单位 ${item.definition.unit}` : ''} · {permissionLabel(item.definition.readable, item.definition.writable, item.definition.notifiable)}{item.valueStatus.observedAt ? ` · ${new Date(item.valueStatus.observedAt).toLocaleTimeString('zh-CN')}` : ''}</small>{item.definition.type === 'enum' && item.definition.enum?.length ? <small className="enum-domain-inline">来源枚举：{item.definition.enum.join(' / ')}</small> : null}{item.valueStatus.error && <small className="catalog-error">{item.valueStatus.error}</small>}</button>
  const tuyaStandardSources = sources.filter((item) => item.capabilityId !== 'tuya-dp')
  const tuyaRawSources = sources.filter((item) => item.capabilityId === 'tuya-dp')
  const groupTuyaSources = catalogMetadata.specType === 'tuya-dp' && tuyaStandardSources.length > 0 && tuyaRawSources.length > 0
  return <section className="binding-manager mapping-graph">
    <div className="profile-heading"><div><p className="eyebrow">属性映射 · {device.providerId}</p><h3><HelpTooltip label="属性映射说明" content={consumerOnly ? '选择模型属性和目标属性，可按需添加转换规则。' : providerOnly ? '同路径、同类型的属性会自动映射。修改只影响当前设备，保存后立即生效。' : '来源先映射到统一模型，再映射到目标。两段映射可分别配置。'}>{consumerLabel ?? `${device.name} · 属性映射`}</HelpTooltip></h3></div><span>{effectiveRouteCount} / {displayedRouteCount} 生效</span></div>
    {!consumerOnly && !providerOnly && <div className="mapping-stage-tabs" role="tablist"><button className={stage === 'provider' ? 'is-active' : ''} onClick={() => setStage('provider')}>① 来源 → 统一模型</button><button className={stage === 'consumer' ? 'is-active' : ''} onClick={() => setStage('consumer')}>② 统一模型 → 目标</button></div>}
    {error && <p className="inline-error" role="alert">{error}</p>}
    <div className={`mapping-lanes is-${stage}-stage`}>
      {stage === 'provider' && <><section className="mapping-lane"><header><strong><HelpTooltip label="来源属性说明" content={lockedProviderSourceKey ? '编辑时不能更换来源属性；如需更换，请取消编辑后新建映射。' : '选择要映射的设备属性。'}>{catalogMetadata.complete ? '来源属性' : '来源属性（不完整）'}</HelpTooltip></strong><small>{sources.length} 属性 · {sourceCommands.length} 动作 · {sourceEvents.length} 事件</small><span className={`catalog-status ${catalogMetadata.complete ? 'is-complete' : 'is-incomplete'}`}>{catalogMetadata.complete ? `完整 · ${catalogMetadata.source}` : `不完整 · ${catalogMetadata.source}`}</span>{catalogMetadata.specType && <code>{catalogMetadata.specType}</code>}{catalogMetadata.error && <small className="catalog-error">{catalogMetadata.error}</small>}</header>
	        <div className="mapping-node-list">{groupTuyaSources ? <><section className="source-property-group" aria-label="Tuya 标准语义属性"><h4><HelpTooltip label="标准属性说明" content="建议优先使用这些属性进行映射。">标准属性</HelpTooltip></h4>{tuyaStandardSources.map(renderSourceProperty)}</section><details className="source-property-group is-raw"><summary>原始 Tuya DP（{tuyaRawSources.length}）</summary>{tuyaRawSources.map(renderSourceProperty)}</details></> : sources.map(renderSourceProperty)}</div>{(sourceCommands.length > 0 || sourceEvents.length > 0) && <details className="source-definition-summary"><summary>动作与事件</summary>{sourceCommands.map(({ endpoint, capability, command }) => <div key={`${endpoint.id}/${capability.id}/${command.id}`}><b>动作（Action）· {command.name}</b><code>{endpoint.id}.{capability.id}.{command.id}</code><small>{command.parameters?.length ?? 0} 个输入参数</small></div>)}{sourceEvents.map(({ endpoint, capability, event }) => <div key={`${endpoint.id}/${capability.id}/${event.id}`}><b>事件（Event）· {event.name}</b><code>{endpoint.id}.{capability.id}.{event.id}</code><small>{valueTypeLabel(event.payload)}</small></div>)}</details>}
      </section>
      <div className="mapping-arrow"><span>→</span><small>{profileId ? profileLabel(profileId) : '不转换'}</small></div></>}
      <section className="mapping-lane is-model"><header><strong><HelpTooltip label="统一模型属性说明" content="属性按端点、能力分组，作为来源与目标之间的统一格式。">统一模型</HelpTooltip></strong><label>当前设备<input aria-label="当前映射设备" value={`${device.name} · ${device.providerId} / ${device.id}`} disabled /></label><div className="mapping-model-controls"><label>设备模型<select aria-label="统一设备模型" value={effectiveType} disabled>{catalog.models.map((item) => <option key={item.deviceType} value={item.deviceType}>{deviceTypeLabel(item.deviceType)}</option>)}</select></label>{stage === 'consumer' && <label className="mapping-model-filter"><span className="mapping-model-filter-heading"><HelpTooltip label="属性筛选说明" content="已绑定发布者的属性，可接收来源设备的状态。">属性筛选</HelpTooltip><small className="mapping-model-filter-count">{visibleParameters.length} / {parameters.length} 个属性可见</small></span><select aria-label="发布者属性筛选" value={modelPropertyFilter} onChange={(event) => setModelPropertyFilter(event.target.value as 'all' | 'publisher-bound')}><option value="all">全部统一模型属性（{parameters.length}）</option><option value="publisher-bound">仅已绑定发布者（{publisherBoundParameterCount}）</option></select></label>}</div></header>
        <div className="mapping-node-list">{visibleParameters.map((item) => {
          const key = pathKey(item.path)
          const publisherBound = stage === 'consumer' && publisherBoundModelKeys.has(key)
          return <button key={key} style={selectablePropertyCardStyle} className={`${key === modelKey ? 'is-selected' : ''}${publisherBound ? ' is-publisher-bound' : ''}`.trim()} onClick={() => setModelKey(key)}><span className={`parameter-level is-${item.level}`}>{parameterLevelLabel(item.level)}</span>{publisherBound && <span className="publisher-bound-mark">已绑定发布者</span>}<strong>{item.name || propertyDisplayLabel('', item.path.propertyId).split('（')[0]}</strong><code>{item.path.endpointId} / {item.path.capabilityId} / {item.path.propertyId}</code><small>{valueTypeLabel(item.type)}{item.unit ? ` · 单位 ${item.unit}` : ''} · {permissionLabel(item.readable, item.writable, item.notifiable)}</small>{item.type === 'enum' && item.enum?.length ? <small className="enum-domain-inline">模型枚举：{item.enum.join(' / ')}</small> : null}</button>
        })}{stage === 'consumer' && modelPropertyFilter === 'publisher-bound' && visibleParameters.length === 0 && <p className="mapping-filter-empty">当前设备没有已绑定发布者的统一模型属性，请先配置第一段路由。</p>}</div>
      </section>
      {stage === 'consumer' && <>
      <div className="mapping-arrow"><span>→</span><small>{profileId ? profileLabel(profileId) : '不转换'}</small></div>
	      <section className="mapping-lane">
	        <header>
	          <span>目标属性</span>
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
      <div className="mapping-profile-field"><label>转换规则<select aria-label="映射转换 Profile" value={profileId} onChange={(event) => { setAutomaticProfileID(null); setProfileId(event.target.value) }}><option value="">不转换</option>{compatibleProfiles.map((item) => <option key={item.id} value={item.id}>{item.identifier ?? item.id} · {(item.transforms ?? []).map((transform) => transform.type).join(' → ') || 'identity'}</option>)}</select></label><button type="button" className="mapping-profile-refresh" aria-label="刷新转换配置列表" onClick={() => void refresh()} disabled={saving}>刷新</button></div>
      {stage === 'provider' && <div className="mapping-readback-config"><label className="mapping-readback-toggle"><input aria-label="为当前属性启用写后回读" type="checkbox" checked={readbackEnabled} disabled={!source?.definition.readable || !source?.definition.writable} onChange={(event) => setReadbackEnabled(event.target.checked)} /><HelpTooltip label="写后回读说明" content="发送控制指令后，在指定时点重新读取当前属性。其他属性不受影响。">写后回读</HelpTooltip></label><label>回读时点（秒）<input aria-label="当前属性写后回读时点" type="text" value={readbackDelays} disabled={!readbackEnabled} placeholder="0.5, 2, 5, 10" onChange={(event) => setReadbackDelays(event.target.value)} /></label>{!(source?.definition.readable && source?.definition.writable) && <small>需要属性同时支持读写</small>}</div>}
		{sourceHasStep && <div className="mapping-readback-config"><label className="mapping-readback-toggle"><input aria-label="为当前属性启用自定义步长" type="checkbox" checked={presentationStepEnabled} onChange={(event) => { setPresentationStepEnabled(event.target.checked); if (event.target.checked && !presentationStep) setPresentationStep(String(normalizePresentationStep(presentationSourceStep ?? minimumPresentationStep, presentationSourceStep))) }} /><HelpTooltip label="映射步长说明" content={`须为来源步长 ${presentationSourceStep} 的整数倍。仅影响目标端显示，设备控制精度不变。`}>自定义步长</HelpTooltip></label><label>映射步长（{modelParameter?.unit ?? source?.definition.unit ?? '无单位'}）<input aria-label="当前属性映射步长" type="number" min={presentationMinimumStep} step={presentationSourceStep} value={presentationStep} disabled={!presentationStepEnabled} placeholder={`来源步长 ${presentationSourceStep}；最小 ${presentationMinimumStep}`} onChange={(event) => setPresentationStep(event.target.value)} /></label></div>}
      <div className="mapping-route-actions"><small>{editingDefaultKey ? '正在编辑默认映射' : editingID ? '正在编辑映射' : inputType && outputType ? `${valueTypeLabel(inputType)} → ${valueTypeLabel(outputType)}` : '请选择两端属性'}</small>{showProfileJump && enumCompatibility.kind === 'none' && <button type="button" onClick={openProfileForCurrentMismatch}>新建转换规则</button>}{(editingID || editingDefaultKey) && <button onClick={clearEditing}>取消编辑</button>}<button className="add-button" disabled={saving || !modelParameter || (stage === 'provider' ? !source : !consumer || !consumerDevice) || (!profileId && inputType !== outputType) || enumProfileRequired} onClick={() => void save()}>{saving ? '保存中…' : editingDefaultKey ? '保存映射' : editingID ? '保存映射' : '＋ 添加映射'}</button></div>
      {showNumericRange && <div className="numeric-range-comparison" role="status"><header><strong>数值范围</strong><span>{activePresentationStep === undefined ? '最终范围由两端约束取交集' : `已应用${stage === 'provider' ? '当前' : '上游'}映射步长 ${activePresentationStep}`}</span></header><section><small>{stage === 'provider' ? '来源范围（Provider）' : activePresentationStep === undefined ? '统一模型范围（Model）' : '统一模型范围（已应用上游映射）'}</small><code>{numericRangeText(numericSource)}</code></section><i>→</i><section><small>转换后范围</small><code>{projectedNumericSource ? numericRangeText(projectedNumericSource) : '无法静态推导'}</code></section><i>∩</i><section><small>{stage === 'provider' ? '统一模型范围（本映射）' : `${consumer?.consumer.name ?? '消费端'}范围（Consumer）`}</small><code>{numericRangeText(routeNumericTarget)}</code></section><i>=</i><section className={effectiveNumericRange ? 'is-effective' : 'is-empty'}><small>最终有效范围</small><code>{effectiveNumericRange ? numericRangeText(effectiveNumericRange) : '无有效交集'}</code></section></div>}
      {enumCompatibility.kind !== 'none' && <div className={`enum-compatibility is-${profileId ? 'profile' : enumCompatibility.kind}`} role="status"><header><strong>枚举检查</strong><span>{profileId ? `由 Profile ${selectedProfile?.identifier ?? profileId} 转换` : enumCompatibility.kind === 'exact' ? '完全一致，可直接映射' : enumCompatibility.kind === 'normalized' ? '仅格式差异，可自动对齐' : enumCompatibility.kind === 'partial' ? enumPartialLabel : '语义不一致，需要 Profile'}</span></header><div className="enum-domain-comparison"><section><small>{enumSourceLabel}</small><div>{enumCompatibility.source.map((item) => <code key={item}>{item}</code>)}</div></section><i>→</i><section><small>{enumTargetLabel}</small><div>{enumCompatibility.target.map((item) => <code key={item}>{item}</code>)}</div></section></div><div className="enum-pair-list">{enumCompatibility.pairs.map((item) => <span key={`${item.source}/${item.target}`}><code>{item.source}</code> → <code>{item.target}</code></span>)}</div>{selectedEnumTransforms.length > 0 && <section className="enum-profile-mappings" aria-label="转换配置映射详情"><header><strong>所选转换配置的枚举映射</strong><span>{selectedProfile?.identifier ?? selectedProfile?.id}</span></header>{selectedEnumTransforms.map((transform) => <section className="enum-profile-transform" key={transform.index}><small>步骤 {transform.index + 1} · 枚举映射</small><div className="enum-profile-pairs"><section><small>正向状态（来源 → 目标）</small><div className="enum-pair-list">{transform.forward.map(([source, target]) => <span key={`${source}/${target}`}><code>{source}</code> → <code>{target}</code></span>)}</div></section>{transform.reverse.length > 0 && <section className="is-reverse"><small>反向控制（目标 → 来源）</small><div className="enum-pair-list">{transform.reverse.map(([target, source]) => <span key={`${target}/${source}`}><code>{target}</code> → <code>{source}</code></span>)}</div></section>}</div></section>)}</section>}{!profileId && enumCompatibility.targetOnly.length > 0 && <p>{enumTargetOnlyLabel}：<code>{enumCompatibility.targetOnly.join(' / ')}</code>；{enumTargetOnlyHint}</p>}{!profileId && enumCompatibility.sourceOnly.length > 0 && <p>无法自动对齐：<code>{enumCompatibility.sourceOnly.join(' / ')}</code>；请选择枚举转换 Profile 后保存。</p>}{showProfileJump && <div className="mapping-profile-jump"><button type="button" className="add-button" onClick={openProfileForCurrentMismatch}>新建转换规则</button><HelpTooltip label="新建转换规则说明" content="在新标签页打开规则编辑器，自动填入当前属性的类型和枚举值。">预填当前属性</HelpTooltip></div>}</div>}
    </div>
    {suggestedCapabilityProfileID === profileId && <p className="capability-profile-suggestion" role="status">已按两端单位自动选择 Capability Profile：<code>{profileLabel(profileId)}</code>。可在下方改为其他转换。</p>}
    {canPreviewHAPValue && consumer && hapPreviewInput && <section className="hap-value-preview" aria-label="HomeKit 属性逐值结果预览">
      <header><strong>HomeKit 属性逐值结果预览</strong><span>{consumerPropertyLabel(consumer.property.id)} · {profileLabel(profileId)}</span></header>
      <div><section><small>统一模型当前值</small><code>{propertyValueText(hapPreviewInput)} · {valueTypeLabel(hapPreviewInput.type)}</code></section><i>→</i><section className={hapPreviewValue ? 'is-output' : ''}><small>HomeKit 结果</small><code>{hapPreviewValue ? `${propertyValueText(hapPreviewValue)} · ${valueTypeLabel(hapPreviewValue.type)}` : '尚未预览'}</code></section><button type="button" onClick={() => void previewHAPValue()} disabled={hapPreviewing}>{hapPreviewing ? '计算中…' : '预览当前值'}</button></div>
      {hapPreviewError && <p role="alert">{hapPreviewError}</p>}
    </section>}
    {recentlyDeletedRoute && <section className="mapping-route-deleted" role="status" aria-label="已删除映射路由">
      <span>刚刚删除</span>
      <div>
        <strong>{recentlyDeletedRoute.targetId && recentlyDeletedRoute.consumerDeviceId ? `${recentlyDeletedRoute.targetId} / ${recentlyDeletedRoute.consumerDeviceId}` : `${recentlyDeletedRoute.providerId} / ${recentlyDeletedRoute.deviceId}`}</strong>
        <code>{recentlyDeletedRoute.stage === 'provider' ? `${recentlyDeletedRoute.endpointId}.${recentlyDeletedRoute.capabilityId}.${recentlyDeletedRoute.propertyId}` : `${recentlyDeletedRoute.modelEndpointId}.${recentlyDeletedRoute.modelCapabilityId}.${recentlyDeletedRoute.modelPropertyId}`} → {recentlyDeletedRoute.stage === 'provider' ? `${recentlyDeletedRoute.modelEndpointId}.${recentlyDeletedRoute.modelCapabilityId}.${recentlyDeletedRoute.modelPropertyId}` : `${recentlyDeletedRoute.consumerId}.${recentlyDeletedRoute.consumerProperty}`}</code>
        <small>已从当前设备路由移除，不再参与映射。</small>
      </div>
    </section>}
    <div className="mapping-route-list"><div className="command-heading"><h3><HelpTooltip label="映射优先级说明" content="手动映射优先于同一目标的默认映射。同一来源可映射到多个不同属性。">当前映射</HelpTooltip></h3><span>{visibleDefaultProviderRoutes.length > 0 ? `${visibleDefaultProviderRoutes.length} 条模型默认 · ` : ''}手动映射 · {targetId && consumerDeviceId ? `${targetId} / ${consumerDeviceId}` : `${device.providerId} / ${device.id}`}</span></div>{visibleDefaultProviderRoutes.map((item) => <article key={`default-${item.key}`} className="is-default"><span className="route-stage is-default">默认映射</span><div><strong>{item.source.deviceName}</strong><code>{item.source.endpointId}.{item.source.capabilityId}.{item.source.propertyId} → {item.model.path.endpointId}.{item.model.path.capabilityId}.{item.model.path.propertyId}</code><small>{deviceTypeLabel(item.source.deviceType)} · 不转换 · {item.source.definition.step ? `原始步长 ${item.source.definition.step}${item.source.definition.unit ? ` ${item.source.definition.unit}` : ''} · ` : ''}默认</small></div><div><button aria-label={`编辑默认映射 ${item.source.propertyId}`} onClick={() => editDefault(item)}>编辑覆盖</button></div></article>)}{listedBindings.map((item) => <article key={item.id} className={item.enabled ? '' : 'is-disabled'}><span className={`route-stage is-${item.stage}`}>{item.stage === 'provider' ? '来源 → 模型' : '模型 → 目标'}</span><div><strong>{item.targetId && item.consumerDeviceId ? `${item.targetId} / ${item.consumerDeviceId}` : `${item.providerId} / ${item.deviceId}`}</strong><code>{item.stage === 'provider' ? `${item.endpointId}.${item.capabilityId}.${item.propertyId}` : `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}`} → {item.stage === 'provider' ? `${item.modelEndpointId}.${item.modelCapabilityId}.${item.modelPropertyId}` : `${item.consumerId}.${item.consumerProperty}`}</code><small>{item.deviceType ? deviceTypeLabel(item.deviceType) : '设备类型未指定'} · {profileLabel(item.profileId)} · {item.enabled ? '实时生效' : '已停用'}{item.stage === 'provider' ? item.readbackEnabled ? ` · 写后回读 ${readbackDelayText(item.readbackDelaysMs)} 秒` : ' · 写后回读关闭' : ''}{item.stage === 'provider' && item.presentationStep != null ? ` · 自定义步长 ${item.presentationStep}` : ''}</small></div><div><button aria-label={`编辑映射路由 ${item.id}`} onClick={() => edit(item)}>编辑</button><button onClick={() => void toggle(item)}>{item.enabled ? '停用' : '启用'}</button><button className="danger-link" onClick={() => void remove(item)}>删除</button></div></article>)}{listedBindings.length === 0 && visibleDefaultProviderRoutes.length === 0 && <p className="mapping-route-empty">暂无映射，请选择两端属性后添加。</p>}</div>
  </section>
}
