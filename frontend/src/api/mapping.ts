import { requestData } from './client'
import type { MappingPreviewRequest, MappingPreviewResult, MappingProfile, MappingProfileInfo } from '../types/mapping'

export const previewMapping = (input: MappingPreviewRequest): Promise<MappingPreviewResult> => requestData('/api/v1/mapping/preview', { method: 'POST', body: JSON.stringify(input) })
export const listMappingProfiles = (): Promise<MappingProfileInfo[]> => requestData('/api/v1/mapping/profiles')
export const createMappingProfile = (profile: MappingProfile): Promise<MappingProfileInfo> => requestData('/api/v1/mapping/profiles', { method: 'POST', body: JSON.stringify(profile) })
export const updateMappingProfile = (id: string, profile: MappingProfile): Promise<MappingProfileInfo> => requestData(`/api/v1/mapping/profiles/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(profile) })
export const deleteMappingProfile = (id: string): Promise<void> => requestData(`/api/v1/mapping/profiles/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const importMappingProfiles = (profiles: MappingProfile[]): Promise<MappingProfileInfo[]> => requestData('/api/v1/mapping/profiles/import', { method: 'POST', body: JSON.stringify({ profiles }) })
