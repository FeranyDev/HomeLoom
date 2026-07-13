import type { DeviceCommand, Diagnostics, RuntimeSettings } from '../types/diagnostics'
import { requestData } from './client'
export const getDiagnostics = (signal?: AbortSignal) => requestData<Diagnostics>('/api/v1/diagnostics', { signal })
export const listCommands = (signal?: AbortSignal) => requestData<DeviceCommand[]>('/api/v1/commands', { signal })
export const getRuntimeSettings = (signal?: AbortSignal) => requestData<RuntimeSettings>('/api/v1/system/settings', { signal })
export const saveRuntimeSettings = (settings: RuntimeSettings) => requestData<RuntimeSettings>('/api/v1/system/settings', { method: 'PUT', body: JSON.stringify(settings) })

export function subscribeCommands(onCommand: (command: DeviceCommand) => void): () => void {
  const source = new EventSource('/api/v1/events/commands')
  source.addEventListener('command', (event) => { try { onCommand(JSON.parse(event.data) as DeviceCommand) } catch { /* ignore malformed events */ } })
  return () => source.close()
}
