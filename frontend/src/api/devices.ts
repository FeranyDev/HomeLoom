import type { Device, DeviceAvailability, Property, PropertyValue, StateValue } from '../types/device'
import { requestData } from './client'

export async function listDevices(signal?: AbortSignal): Promise<Device[]> {
  return requestData<Device[]>('/api/v1/devices', { signal })
}

export async function setDeviceEnabled(id: string, enabled: boolean): Promise<Device> {
	return requestData<Device>(`/api/v1/devices/${encodeURIComponent(id)}/enabled`, { method: 'PUT', body: JSON.stringify({ enabled }) })
}

export async function getDeviceStates(id: string, signal?: AbortSignal): Promise<StateValue[]> {
  return requestData<StateValue[]>(`/api/v1/devices/${encodeURIComponent(id)}/states`, { signal })
}

export async function getDeviceProperty(id: string, endpointId: string, capabilityId: string, propertyId: string, signal?: AbortSignal): Promise<Property> {
  return requestData<Property>(`/api/v1/devices/${encodeURIComponent(id)}/endpoints/${encodeURIComponent(endpointId)}/capabilities/${encodeURIComponent(capabilityId)}/properties/${encodeURIComponent(propertyId)}`, { signal })
}

export function subscribeDeviceStates(id: string, onState: (state: StateValue) => void): () => void {
  const source = new EventSource(`/api/v1/events/states?deviceId=${encodeURIComponent(id)}`)
  source.addEventListener('state', (event) => { try { onState(JSON.parse(event.data) as StateValue) } catch { /* ignore malformed events */ } })
  return () => source.close()
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

export function subscribeDevices(onDevice: (device: Device) => void, onConnection?: (connected: boolean) => void): () => void {
  const source = new EventSource('/api/v1/events/devices')
  source.addEventListener('ready', () => onConnection?.(true))
  source.addEventListener('device', (event) => { try { onDevice(JSON.parse(event.data) as Device) } catch { /* ignore malformed stream events */ } })
  source.onerror = () => onConnection?.(false)
  return () => source.close()
}
