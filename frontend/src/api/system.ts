import { requestData } from './client'
import type { SystemVersion } from '../types/diagnostics'

export function getSystemVersion(signal?: AbortSignal): Promise<SystemVersion> {
  return requestData<SystemVersion>('/api/v1/system/version', { signal })
}
