export type DeviceType = 'switch' | 'temperature-sensor' | 'lightbulb' | 'outlet' | 'humidity-sensor' | 'contact-sensor' | 'motion-sensor' | 'fan' | 'air-purifier' | 'window-covering'
export type ValueType = 'bool' | 'int' | 'number' | 'string' | 'enum'
export type DeviceAvailability = 'online' | 'offline' | 'unknown'
export type ParameterLevel = 'required' | 'optional' | 'custom'

export interface PropertyValue { type: ValueType; bool?: boolean; int?: number; number?: number; string?: string }
export interface PropertyDefinition { id: string; name: string; type: ValueType; parameterLevel?: ParameterLevel; unit?: string; readable: boolean; writable: boolean; notifiable: boolean; min?: number; max?: number; step?: number; enum?: string[]; staleAfterSeconds?: number }
export interface Property { definition: PropertyDefinition; value: PropertyValue }
export interface CommandParameter { id: string; name: string; type: ValueType; required: boolean }
export interface CommandDefinition { id: string; name: string; idempotent?: boolean; parameters?: CommandParameter[] }
export interface Capability { id: string; type: string; properties: Property[]; commands?: CommandDefinition[]; events?: { id: string; name: string; payload: ValueType }[] }
export interface Endpoint { id: string; name: string; type: string; capabilities: Capability[] }
export interface Device {
  schemaVersion: number; id: string; providerId: string; name: string; type: DeviceType; availability: DeviceAvailability; online: boolean
  sequence?: number; disabled?: boolean; removed?: boolean; endpoints: Endpoint[]; lastUpdateAt: string
}

export function availabilityLabel(value: DeviceAvailability): string { return value === 'online' ? '在线' : value === 'offline' ? '离线' : '未知' }

export function deviceProperty(device: Device, capabilityId: string, propertyId: string): PropertyValue | undefined {
  return device.endpoints.flatMap((endpoint) => endpoint.capabilities).find((capability) => capability.id === capabilityId)?.properties.find((property) => property.definition.id === propertyId)?.value
}

export interface StateValue {
  key: { deviceId: string; endpointId: string; capabilityId: string; propertyId: string }
  value: { kind: 'bool' | 'int' | 'number' | 'string' | 'enum'; bool?: boolean; int?: number; number?: number; string?: string } | null
  providerId: string; source: string; observedAt: string; receivedAt: string; expiresAt?: string
  sequence: number; version: number; quality: string; known: boolean; available: boolean; unavailableReason?: string; traceId?: string; pendingCommandId?: string
}
