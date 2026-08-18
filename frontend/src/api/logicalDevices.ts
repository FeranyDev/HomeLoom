import { requestData, requestJSON } from './client'
import type { LogicalDeviceCandidate, LogicalDeviceConfig, LogicalRouteExplanation } from '../types/logicalDevice'

export function listLogicalDevices(signal?: AbortSignal): Promise<LogicalDeviceConfig[]> {
  return requestData<LogicalDeviceConfig[]>('/api/v1/logical-devices', { signal })
}
export function listLogicalDeviceCandidates(signal?: AbortSignal): Promise<LogicalDeviceCandidate[]> {
  return requestData<LogicalDeviceCandidate[]>('/api/v1/logical-devices/candidates', { signal })
}
export function saveLogicalDevice(item: LogicalDeviceConfig, editing: boolean): Promise<LogicalDeviceConfig> {
  const path = editing ? `/api/v1/logical-devices/${encodeURIComponent(item.id)}` : '/api/v1/logical-devices'
  return requestData<LogicalDeviceConfig>(path, { method: editing ? 'PUT' : 'POST', body: JSON.stringify(item) })
}
export async function deleteLogicalDevice(id: string): Promise<void> {
  await requestJSON<void>(`/api/v1/logical-devices/${encodeURIComponent(id)}`, { method: 'DELETE' })
}
export function getLogicalDeviceExplanations(id: string, signal?: AbortSignal): Promise<LogicalRouteExplanation[]> {
  return requestData<LogicalRouteExplanation[]>(`/api/v1/logical-devices/${encodeURIComponent(id)}/explanations`, { signal })
}
