export type DeviceType = 'switch' | 'temperature-sensor' | 'lightbulb' | 'outlet' | 'humidity-sensor' | 'contact-sensor' | 'motion-sensor'
export type ValueType = 'bool' | 'number' | 'string' | 'enum'

export interface PropertyValue { type: ValueType; bool?: boolean; number?: number; string?: string }
export interface PropertyDefinition { id: string; name: string; type: ValueType; unit?: string; readable: boolean; writable: boolean; notifiable: boolean; min?: number; max?: number; step?: number; enum?: string[]; staleAfterSeconds?: number }
export interface Property { definition: PropertyDefinition; value: PropertyValue }
export interface Capability { id: string; type: string; properties: Property[]; commands?: { id: string; name: string }[]; events?: { id: string; name: string; payload: ValueType }[] }
export interface Endpoint { id: string; name: string; type: string; capabilities: Capability[] }
export interface Device {
  schemaVersion: number; id: string; providerId: string; name: string; type: DeviceType; online: boolean
  endpoints: Endpoint[]; lastUpdateAt: string
}

export function deviceProperty(device: Device, capabilityId: string, propertyId: string): PropertyValue | undefined {
  return device.endpoints.flatMap((endpoint) => endpoint.capabilities).find((capability) => capability.id === capabilityId)?.properties.find((property) => property.definition.id === propertyId)?.value
}

export interface StateValue {
  key: { deviceId: string; endpointId: string; capabilityId: string; propertyId: string }
  value: { kind: 'bool' | 'number'; bool?: boolean; number?: number }
  providerId: string; source: string; observedAt: string; receivedAt: string; expiresAt?: string
  sequence: number; version: number; quality: string; pendingCommandId?: string
}
