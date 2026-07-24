import type { Device, DeviceType, ParameterLevel, PropertyDefinition, PropertyValue, ValueType } from './device'

export type MappingDirection = 'forward' | 'reverse'
export type MappingTransformType = 'invert' | 'reciprocal' | 'int-number' | 'scale' | 'clamp' | 'enum' | 'unit' | 'range-enum' | 'enum-number' | 'threshold' | 'bool-number' | 'bool-enum' | 'enum-bool' | 'map-range' | 'round' | 'parse-number' | 'number-string'

export interface MappingRangeBand { max?: number; value: string; reverse: number }

export interface MappingTransform {
  type: MappingTransformType
  factor?: number
  offset?: number
  min?: number
  max?: number
  values?: Record<string, string>
  reverseValues?: Record<string, string>
  fromUnit?: string
  toUnit?: string
  bands?: MappingRangeBand[]
  threshold?: number
  operator?: 'gte' | 'gt' | 'lte' | 'lt'
  trueNumber?: number
  falseNumber?: number
  trueValue?: string
  falseValue?: string
  inputMin?: number
  inputMax?: number
  outputMin?: number
  outputMax?: number
  mode?: 'nearest' | 'floor' | 'ceil'
}

export interface MappingProfile {
  schemaVersion: 1
  id: string
  version: number
  kind: 'provider' | 'capability' | 'target'
  inputType: ValueType
  outputType: ValueType
  default?: PropertyValue
  transforms: MappingTransform[]
}

export interface MappingProfileInfo extends MappingProfile { builtIn: boolean }

export interface MappingBinding {
  id: string; stage: 'provider' | 'consumer'; profileId?: string; enabled: boolean
  providerId?: string; deviceId?: string; endpointId?: string; capabilityId?: string; propertyId?: string
  deviceType?: DeviceType; consumerDeviceType?: DeviceType; modelEndpointId: string; modelCapabilityId: string; modelPropertyId: string
  consumerId?: string; consumerProperty?: string
	targetId?: string; consumerDeviceId?: string
}

export interface ModelPath { endpointId: string; capabilityId: string; propertyId: string }
export interface ModelParameter {
  path: ModelPath; name: string; level: ParameterLevel; type: ValueType; unit?: string
  readable: boolean; writable: boolean; notifiable: boolean; enum?: string[]
  min?: number; max?: number; step?: number; staleAfterSeconds?: number
  publisher: { level: ParameterLevel; behavior: string }; consumer: { level: ParameterLevel; behavior: string }
  publisherNotes?: string; consumerNotes?: string
}
export interface ModelContract { deviceType: DeviceType; name?: string; version: number; builtIn: boolean; parameters: ModelParameter[]; custom: { publisher: { level: ParameterLevel; behavior: string }; consumer: { level: ParameterLevel; behavior: string } } }
export interface CustomModel { deviceType: DeviceType; name: string; version: number }
export interface ConsumerProperty {
  id: string; name: string; deviceType: DeviceType; defaultModelPath: ModelPath; level: ParameterLevel; type: ValueType
  unit?: string; min?: number; max?: number; step?: number; enum?: string[]
  readable: boolean; writable: boolean; notifiable: boolean
	/** Matter catalog entries identify the protocol member without overloading a HomeKit service path. */
	originalName?: string
	cluster?: string
	/** Canonical server field for the Attribute or Command name. */
	element?: string
	/** Transitional alias accepted from early Matter catalog payloads. */
	member?: string
	kind?: 'attribute' | 'command'
}
export interface ConsumerCatalog { id: string; name: string; properties: ConsumerProperty[] }
export interface SourceCatalogMetadata {
  complete: boolean; source: string; specType?: string; model?: string; fetchedAt?: string; error?: string
  values?: Record<string, SourceValueStatus>
}
export interface SourceValueStatus { known: boolean; available: boolean; observedAt?: string; error?: string }
export interface SourceCatalogDevice extends Device { catalog?: SourceCatalogMetadata }
export interface MappingCatalog { providers: SourceCatalogDevice[]; models: ModelContract[]; consumers: ConsumerCatalog[] }
export interface CustomModelProperty {
  id: string; deviceType: DeviceType; endpointId: string; endpointName: string; endpointType: string
  capabilityId: string; capabilityType: string; definition: PropertyDefinition
}
export interface ModelEnumOverride {
  id?: string
  deviceType: DeviceType
  endpointId: string
  capabilityId: string
  propertyId: string
  enum: string[]
}

export interface MappingPreviewRequest { profileId?: string; profile?: MappingProfile; direction: MappingDirection; value: PropertyValue | null }
export interface MappingStep { index: number; transform: string; input: PropertyValue | null; output: PropertyValue }
export interface MappingPreviewResult { profileId: string; profileVersion: number; direction: MappingDirection; value: PropertyValue; steps: MappingStep[] }
