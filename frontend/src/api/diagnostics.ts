import type { DeviceCommand, Diagnostics } from '../types/diagnostics'
import { requestData } from './client'
export const getDiagnostics = (signal?: AbortSignal) => requestData<Diagnostics>('/api/v1/diagnostics', { signal })
export const listCommands = (signal?: AbortSignal) => requestData<DeviceCommand[]>('/api/v1/commands', { signal })

export function subscribeCommands(onCommand: (command: DeviceCommand) => void): () => void {
  const source = new EventSource('/api/v1/events/commands')
  source.addEventListener('command', (event) => { try { onCommand(JSON.parse(event.data) as DeviceCommand) } catch { /* ignore malformed events */ } })
  return () => source.close()
}
