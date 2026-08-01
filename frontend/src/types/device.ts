export const builtInDeviceTypes = [
  'switch', 'temperature-sensor', 'humidity-sensor', 'temperature-humidity-sensor',
  'pressure-sensor', 'noise-sensor', 'water-level-sensor', 'soil-moisture-sensor', 'lightbulb', 'outlet',
  'contact-sensor', 'motion-sensor', 'fan', 'air-purifier', 'window-covering',
  'illuminance-sensor', 'occupancy-sensor', 'leak-sensor', 'smoke-sensor',
  'carbon-monoxide-sensor', 'carbon-dioxide-sensor', 'air-quality-sensor',
  'thermostat', 'air-conditioner', 'heater-cooler', 'humidifier-dehumidifier', 'lock', 'garage-door',
  'security-system', 'valve', 'pump', 'water-heater', 'power-meter', 'ev-charger',
  'speaker', 'television', 'robot-vacuum',
] as const
type BuiltInDeviceType = typeof builtInDeviceTypes[number]
export type DeviceType = BuiltInDeviceType | (string & {})
export type ValueType = 'bool' | 'int' | 'number' | 'string' | 'enum'
export type DeviceAvailability = 'online' | 'offline' | 'unknown'
export type ParameterLevel = 'required' | 'optional' | 'custom'
export type DeviceRuntimeMode = 'pending' | 'local' | 'cloud'
export type DeviceStateTransport = 'pending' | 'local-mqtt' | 'cloud-mqtt' | 'cloud-http'

export interface PropertyValue { type: ValueType; bool?: boolean; int?: number; number?: number; string?: string }
export interface PropertyDefinition { id: string; name: string; type: ValueType; parameterLevel?: ParameterLevel; unit?: string; readable: boolean; writable: boolean; notifiable: boolean; min?: number; max?: number; step?: number; enum?: string[]; staleAfterSeconds?: number }
export interface Property { definition: PropertyDefinition; value: PropertyValue; stateTransport?: DeviceStateTransport }
interface CommandParameter { id: string; name: string; type: ValueType; required: boolean }
export interface CommandDefinition { id: string; name: string; idempotent?: boolean; parameters?: CommandParameter[] }
interface Capability { id: string; type: string; properties: Property[]; commands?: CommandDefinition[]; events?: { id: string; name: string; payload: ValueType }[] }
interface Endpoint { id: string; name: string; type: string; capabilities: Capability[] }
export interface Device {
  schemaVersion: number; id: string; providerId: string; name: string; type: DeviceType; availability: DeviceAvailability; online: boolean
  homeId?: string; homeName?: string; roomId?: string; roomName?: string
  sequence?: number; disabled?: boolean; removed?: boolean; runtimeMode?: DeviceRuntimeMode; stateTransport?: DeviceStateTransport; endpoints: Endpoint[]; lastUpdateAt: string
}

export function availabilityLabel(value: DeviceAvailability): string { return value === 'online' ? '在线' : value === 'offline' ? '离线' : '未知' }
export function runtimeModeLabel(value: DeviceRuntimeMode, transport?: DeviceStateTransport): string {
  if (transport === 'local-mqtt') return '中枢实时'
  if (transport === 'cloud-mqtt') return '官方云实时'
  if (transport === 'cloud-http') return '官方云校准'
  return value === 'local' ? '局域网' : value === 'cloud' ? '云端轮询' : '等待判定'
}

export function deviceProperty(device: Device, capabilityId: string, propertyId: string): PropertyValue | undefined {
  return (device.endpoints ?? [])
    .flatMap((endpoint) => endpoint.capabilities ?? [])
    .find((capability) => capability.id === capabilityId)
    ?.properties?.find((property) => property.definition.id === propertyId)?.value
}

export interface StateValue {
  key: { deviceId: string; endpointId: string; capabilityId: string; propertyId: string }
  value: { kind: 'bool' | 'int' | 'number' | 'string' | 'enum'; bool?: boolean; int?: number; number?: number; string?: string } | null
  providerId: string; source: string; observedAt: string; receivedAt: string; expiresAt?: string
  sequence: number; version: number; quality: string; known: boolean; available: boolean; unavailableReason?: string; traceId?: string; pendingCommandId?: string
}
