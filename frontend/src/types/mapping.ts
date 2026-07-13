import type { PropertyValue, ValueType } from './device'

export type MappingDirection = 'forward' | 'reverse'
export type MappingTransformType = 'invert' | 'scale' | 'clamp' | 'enum' | 'unit'

export interface MappingTransform {
  type: MappingTransformType
  factor?: number
  offset?: number
  min?: number
  max?: number
  values?: Record<string, string>
  fromUnit?: string
  toUnit?: string
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

export interface MappingPreviewRequest { profileId?: string; profile?: MappingProfile; direction: MappingDirection; value: PropertyValue | null }
export interface MappingStep { index: number; transform: string; input: PropertyValue | null; output: PropertyValue }
export interface MappingPreviewResult { profileId: string; profileVersion: number; direction: MappingDirection; value: PropertyValue; steps: MappingStep[] }
