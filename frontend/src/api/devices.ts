import type { Device, DeviceAvailability, DeviceLocationHome, DeviceLocationMode, DeviceLocationRoom, MCPDeviceConfig, MCPPropertyConfig, Property, PropertyValue, StateValue } from '../types/device'
import { requestData, requestJSON } from './client'
import { subscribeEvents } from './events'

export async function listDevices(signal?: AbortSignal): Promise<Device[]> {
  return requestData<Device[]>('/api/v1/devices', { signal })
}

export async function setDeviceEnabled(id: string, enabled: boolean): Promise<Device> {
	return requestData<Device>(`/api/v1/devices/${encodeURIComponent(id)}/enabled`, { method: 'PUT', body: JSON.stringify({ enabled }) })
}

export async function setDeviceLocation(id: string, input: { mode: DeviceLocationMode; homeId?: string; roomId?: string }): Promise<Device> {
	return requestData<Device>(`/api/v1/devices/${encodeURIComponent(id)}/location`, { method: 'PUT', body: JSON.stringify(input) })
}

export async function listDeviceLocations(signal?: AbortSignal): Promise<DeviceLocationHome[]> {
	return requestData<DeviceLocationHome[]>('/api/v1/locations', { signal })
}

export async function createDeviceLocationHome(name: string): Promise<DeviceLocationHome> {
	return requestData<DeviceLocationHome>('/api/v1/locations/homes', { method: 'POST', body: JSON.stringify({ name }) })
}

export async function updateDeviceLocationHome(id: string, name: string): Promise<DeviceLocationHome> {
	return requestData<DeviceLocationHome>(`/api/v1/locations/homes/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ name }) })
}

export async function deleteDeviceLocationHome(id: string): Promise<void> {
	await requestJSON<void>(`/api/v1/locations/homes/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function createDeviceLocationRoom(homeId: string, name: string): Promise<DeviceLocationRoom> {
	return requestData<DeviceLocationRoom>(`/api/v1/locations/homes/${encodeURIComponent(homeId)}/rooms`, { method: 'POST', body: JSON.stringify({ name }) })
}

export async function updateDeviceLocationRoom(homeId: string, id: string, name: string): Promise<DeviceLocationRoom> {
	return requestData<DeviceLocationRoom>(`/api/v1/locations/homes/${encodeURIComponent(homeId)}/rooms/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ name }) })
}

export async function deleteDeviceLocationRoom(homeId: string, id: string): Promise<void> {
	await requestJSON<void>(`/api/v1/locations/homes/${encodeURIComponent(homeId)}/rooms/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function getDeviceStates(id: string, signal?: AbortSignal): Promise<StateValue[]> {
  return requestData<StateValue[]>(`/api/v1/devices/${encodeURIComponent(id)}/states`, { signal })
}

export async function getDeviceProperty(id: string, endpointId: string, capabilityId: string, propertyId: string, signal?: AbortSignal): Promise<Property> {
  return requestData<Property>(`/api/v1/devices/${encodeURIComponent(id)}/endpoints/${encodeURIComponent(endpointId)}/capabilities/${encodeURIComponent(capabilityId)}/properties/${encodeURIComponent(propertyId)}`, { signal })
}

export async function getDeviceMCPConfig(id: string, signal?: AbortSignal): Promise<MCPDeviceConfig> {
  return requestData<MCPDeviceConfig>(`/api/v1/devices/${encodeURIComponent(id)}/mcp-config`, { signal })
}

export async function saveDeviceMCPConfig(id: string, config: Omit<MCPDeviceConfig, 'deviceId'>): Promise<MCPDeviceConfig> {
  return requestData<MCPDeviceConfig>(`/api/v1/devices/${encodeURIComponent(id)}/mcp-config`, { method: 'PUT', body: JSON.stringify(config) })
}

export async function listDeviceMCPPropertyConfigs(id: string, signal?: AbortSignal): Promise<MCPPropertyConfig[]> {
  return requestData<MCPPropertyConfig[]>(`/api/v1/devices/${encodeURIComponent(id)}/mcp-properties`, { signal })
}

export async function saveDeviceMCPPropertyConfig(id: string, config: Omit<MCPPropertyConfig, 'deviceId' | 'endpointId' | 'capabilityId' | 'propertyId'> & Pick<MCPPropertyConfig, 'endpointId' | 'capabilityId' | 'propertyId'>): Promise<MCPPropertyConfig> {
  return requestData<MCPPropertyConfig>(`/api/v1/devices/${encodeURIComponent(id)}/mcp-properties/${encodeURIComponent(config.endpointId)}/${encodeURIComponent(config.capabilityId)}/${encodeURIComponent(config.propertyId)}`, { method: 'PUT', body: JSON.stringify({ usageNote: config.usageNote, access: config.access, allowUnattendedAi: config.allowUnattendedAi }) })
}

export async function deleteDeviceMCPPropertyConfig(id: string, endpointId: string, capabilityId: string, propertyId: string): Promise<void> {
  await requestJSON<void>(`/api/v1/devices/${encodeURIComponent(id)}/mcp-properties/${encodeURIComponent(endpointId)}/${encodeURIComponent(capabilityId)}/${encodeURIComponent(propertyId)}`, { method: 'DELETE' })
}

export function subscribeDeviceStates(id: string, onState: (state: StateValue) => void): () => void {
	return subscribeEvents({ onState: (state) => { if (state.key.deviceId === id) onState(state) } })
}

export async function setDevicePower(id: string, value: boolean): Promise<Device> {
  return requestData<Device>(`/api/v1/devices/${encodeURIComponent(id)}/properties/power`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ value }),
  })
}

export async function setDeviceProperty(id: string, endpointId: string, capabilityId: string, propertyId: string, value: PropertyValue): Promise<Device> {
  return requestData<Device>(`/api/v1/devices/${encodeURIComponent(id)}/endpoints/${encodeURIComponent(endpointId)}/capabilities/${encodeURIComponent(capabilityId)}/properties/${encodeURIComponent(propertyId)}`, { method: 'PUT', body: JSON.stringify(value) })
}

export async function executeDeviceCommand(id: string, endpointId: string, capabilityId: string, commandId: string, parameters: Record<string, PropertyValue>, idempotencyKey: string): Promise<Device> {
	return requestData<Device>(`/api/v1/devices/${encodeURIComponent(id)}/endpoints/${encodeURIComponent(endpointId)}/capabilities/${encodeURIComponent(capabilityId)}/commands/${encodeURIComponent(commandId)}`, { method: 'POST', headers: { 'Idempotency-Key': idempotencyKey }, body: JSON.stringify({ parameters }) })
}

export async function simulateDevice(id: string, values: { availability?: DeviceAvailability; online?: boolean; power?: boolean; value?: number; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; active?: boolean; speed?: number; mode?: string; filterLife?: number; filterChange?: boolean; position?: number; sequence?: number; repeat?: number }): Promise<Device> {
  return requestData<Device>(`/api/v1/devices/${encodeURIComponent(id)}/simulation`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values),
  })
}
