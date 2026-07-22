import type { AuditEvent, DeviceCommand, Diagnostics, RuntimeSettings } from '../types/diagnostics'
import { requestData } from './client'
import { subscribeEvents, type RuntimeDelta } from './events'
export const getDiagnostics = (signal?: AbortSignal) => requestData<Diagnostics>('/api/v1/diagnostics', { signal })
export const listCommands = (signal?: AbortSignal) => requestData<DeviceCommand[]>('/api/v1/commands', { signal })
export const listAuditEvents = (signal?: AbortSignal) => requestData<AuditEvent[]>('/api/v1/audit-events?limit=200', { signal })
export const getRuntimeSettings = (signal?: AbortSignal) => requestData<RuntimeSettings>('/api/v1/system/settings', { signal })
export const saveRuntimeSettings = (settings: RuntimeSettings) => requestData<RuntimeSettings>('/api/v1/system/settings', { method: 'PUT', body: JSON.stringify(settings) })

export function subscribeCommands(onCommand: (command: DeviceCommand) => void): () => void {
	return subscribeEvents({ onCommand })
}

export function subscribeAuditEvents(onEvent: (event: AuditEvent) => void): () => void {
	return subscribeEvents({ onAudit: onEvent })
}

export function subscribeRuntime(onSnapshot: (snapshot: RuntimeDelta) => void): () => void {
	return subscribeEvents({ onRuntime: onSnapshot })
}
