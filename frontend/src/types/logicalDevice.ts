import type { DeviceType } from './device'

export interface LogicalSourceRef { providerId: string; deviceId: string }
export interface LogicalBinding extends LogicalSourceRef { priority: number }
export interface LogicalPropertyPath { endpointId: string; capabilityId: string; propertyId: string }
export interface LogicalCommandPath { endpointId: string; capabilityId: string; commandId: string }
export interface LogicalPropertyCandidate extends LogicalSourceRef { path: LogicalPropertyPath; priority: number; allowFallback?: boolean }
export interface LogicalCommandCandidate extends LogicalSourceRef { path: LogicalCommandPath; priority: number; allowFallback?: boolean }
export interface LogicalPropertyRoute { path: LogicalPropertyPath; candidates: LogicalPropertyCandidate[] }
export interface LogicalCommandRoute { path: LogicalCommandPath; candidates: LogicalCommandCandidate[] }
export interface LogicalDeviceConfig {
  id: string; name: string; type: DeviceType; bindings: LogicalBinding[]
  propertyRoutes?: LogicalPropertyRoute[]; commandRoutes?: LogicalCommandRoute[]
}
export interface LogicalDeviceCandidate {
  left: LogicalSourceRef & { name: string; type: DeviceType; homeId?: string; roomId?: string }
  right: LogicalSourceRef & { name: string; type: DeviceType; homeId?: string; roomId?: string }
  reasons: string[]
}
export interface LogicalRouteExplanation {
  logicalDeviceId: string; kind: 'property' | 'command'; path: string; reason: string
  selected: { providerId: string; deviceId: string; path: string; priority: number; available: boolean; selected: boolean }
  candidates: Array<{ providerId: string; deviceId: string; path: string; priority: number; available: boolean; selected: boolean }>
}
