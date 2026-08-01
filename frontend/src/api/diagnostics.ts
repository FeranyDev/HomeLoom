import type { AuditEvent, DeviceCommand, Diagnostics, RuntimeSettings, SubprocessLogEntry } from '../types/diagnostics'
import { requestData } from './client'
export const getDiagnostics = (signal?: AbortSignal) => requestData<Diagnostics>('/api/v1/diagnostics', { signal })
export const listCommands = (signal?: AbortSignal) => requestData<DeviceCommand[]>('/api/v1/commands', { signal })
export const listAuditEvents = (signal?: AbortSignal) => requestData<AuditEvent[]>('/api/v1/audit-events?limit=200', { signal })
export const getRuntimeSettings = (signal?: AbortSignal) => requestData<RuntimeSettings>('/api/v1/system/settings', { signal })
export const saveRuntimeSettings = (settings: RuntimeSettings) => requestData<RuntimeSettings>('/api/v1/system/settings', { method: 'PUT', body: JSON.stringify(settings) })
export const listSubprocessLogs = (after = 0, signal?: AbortSignal) => requestData<SubprocessLogEntry[]>(`/api/v1/system/subprocess-logs?after=${after}&limit=500`, { signal })
