import { requestData } from './client'
import type { MappingPreviewRequest, MappingPreviewResult } from '../types/mapping'

export const previewMapping = (input: MappingPreviewRequest): Promise<MappingPreviewResult> => requestData('/api/v1/mapping/preview', { method: 'POST', body: JSON.stringify(input) })
