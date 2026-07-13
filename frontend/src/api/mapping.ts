import { requestData } from './client'
import type { MappingBinding, MappingPreviewRequest, MappingPreviewResult, MappingProfile, MappingProfileInfo } from '../types/mapping'

export const previewMapping = (input: MappingPreviewRequest): Promise<MappingPreviewResult> => requestData('/api/v1/mapping/preview', { method: 'POST', body: JSON.stringify(input) })
export const listMappingProfiles = (): Promise<MappingProfileInfo[]> => requestData('/api/v1/mapping/profiles')
export const createMappingProfile = (profile: MappingProfile): Promise<MappingProfileInfo> => requestData('/api/v1/mapping/profiles', { method: 'POST', body: JSON.stringify(profile) })
export const updateMappingProfile = (id: string, profile: MappingProfile): Promise<MappingProfileInfo> => requestData(`/api/v1/mapping/profiles/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(profile) })
export const deleteMappingProfile = (id: string): Promise<void> => requestData(`/api/v1/mapping/profiles/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const importMappingProfiles = (profiles: MappingProfile[]): Promise<MappingProfileInfo[]> => requestData('/api/v1/mapping/profiles/import', { method: 'POST', body: JSON.stringify({ profiles }) })
export const listMappingBindings = (): Promise<MappingBinding[]> => requestData('/api/v1/mapping/bindings')
export const createMappingBinding = (binding: Omit<MappingBinding, 'id'> & { id?: string }): Promise<MappingBinding> => requestData('/api/v1/mapping/bindings', { method: 'POST', body: JSON.stringify(binding) })
export const updateMappingBinding = (id: string, binding: MappingBinding): Promise<MappingBinding> => requestData(`/api/v1/mapping/bindings/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(binding) })
export const deleteMappingBinding = (id: string): Promise<void> => requestData(`/api/v1/mapping/bindings/${encodeURIComponent(id)}`, { method: 'DELETE' })
