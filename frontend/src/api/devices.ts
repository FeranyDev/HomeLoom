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

