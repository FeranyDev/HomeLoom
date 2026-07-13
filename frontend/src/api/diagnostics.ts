import type { DeviceCommand, Diagnostics } from '../types/diagnostics'

interface ApiResponse<T> { data: T }
async function read<T>(path: string, signal?: AbortSignal): Promise<T> { const response = await fetch(path, { signal }); if (!response.ok) throw new Error(`诊断请求失败 (${response.status})`); return ((await response.json()) as ApiResponse<T>).data }
export const getDiagnostics = (signal?: AbortSignal) => read<Diagnostics>('/api/v1/diagnostics', signal)
export const listCommands = (signal?: AbortSignal) => read<DeviceCommand[]>('/api/v1/commands', signal)
