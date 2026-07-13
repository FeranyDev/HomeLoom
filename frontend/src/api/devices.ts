import type { Device } from '../types/device'

interface ApiResponse<T> {
  data: T
}

async function parse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    throw new Error(`请求失败 (${response.status})`)
  }
  return (await response.json()) as T
}

export async function listDevices(signal?: AbortSignal): Promise<Device[]> {
  const response = await fetch('/api/v1/devices', { signal })
  return (await parse<ApiResponse<Device[]>>(response)).data
}

export async function setDevicePower(id: string, value: boolean): Promise<Device> {
  const response = await fetch(`/api/v1/devices/${encodeURIComponent(id)}/properties/power`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ value }),
  })
  return (await parse<ApiResponse<Device>>(response)).data
}

export async function simulateDevice(id: string, values: { online?: boolean; power?: boolean; temperature?: number }): Promise<Device> {
  const response = await fetch(`/api/v1/devices/${encodeURIComponent(id)}/simulation`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values),
  })
  return (await parse<ApiResponse<Device>>(response)).data
}

export function subscribeDevices(onDevice: (device: Device) => void, onConnection?: (connected: boolean) => void): () => void {
  const source = new EventSource('/api/v1/events/devices')
  source.addEventListener('ready', () => onConnection?.(true))
  source.addEventListener('device', (event) => { try { onDevice(JSON.parse(event.data) as Device) } catch { /* ignore malformed stream events */ } })
  source.onerror = () => onConnection?.(false)
  return () => source.close()
}
