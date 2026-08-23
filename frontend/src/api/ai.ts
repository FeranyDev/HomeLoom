import { requestData } from './client'
import type { AIAutomation, AIAutomationInput, AIAutomationRun, AIModel, AIRun, AIServiceInput, AIServiceStatus } from '../types/ai'

export const getAIServiceConfig = () => requestData<AIServiceStatus>('/api/v1/ai-service/config')
export const saveAIServiceConfig = (input: AIServiceInput) => requestData<AIServiceStatus>('/api/v1/ai-service/config', { method: 'PUT', body: JSON.stringify(input) })
export const listAIModels = () => requestData<AIModel[]>('/api/v1/ai-service/models')
export const startAIRun = (message: string) => requestData<AIRun>('/api/v1/ai-service/runs', { method: 'POST', body: JSON.stringify({ message }) })
export const getAIRun = (id: string) => requestData<AIRun>(`/api/v1/ai-service/runs/${encodeURIComponent(id)}`)
export const approveAIRun = (id: string) => requestData<AIRun>(`/api/v1/ai-service/runs/${encodeURIComponent(id)}/approve`, { method: 'POST' })
export const listAIAutomations = () => requestData<AIAutomation[]>('/api/v1/ai-service/automations')
export const createAIAutomation = (input: AIAutomationInput) => requestData<AIAutomation>('/api/v1/ai-service/automations', { method: 'POST', body: JSON.stringify(input) })
export const saveAIAutomation = (id: string, input: AIAutomationInput) => requestData<AIAutomation>(`/api/v1/ai-service/automations/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(input) })
export const deleteAIAutomation = (id: string) => requestData<void>(`/api/v1/ai-service/automations/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const runAIAutomation = (id: string) => requestData<{ automation: AIAutomation, run: AIAutomationRun }>(`/api/v1/ai-service/automations/${encodeURIComponent(id)}/run`, { method: 'POST' })
export const approveAIAutomationRun = (automationID: string, runID: string) => requestData<{ automation: AIAutomation, run: AIAutomationRun }>(`/api/v1/ai-service/automations/${encodeURIComponent(automationID)}/runs/${encodeURIComponent(runID)}/approve`, { method: 'POST' })
