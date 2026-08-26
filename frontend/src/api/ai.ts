import { requestData, requestStream } from './client'
import type { AIAutomation, AIAutomationInput, AIAutomationRun, AIConversationTurn, AIModel, AIRun, AIRunStreamEvent, AIServiceInput, AIServiceStatus } from '../types/ai'

export const getAIServiceConfig = () => requestData<AIServiceStatus>('/api/v1/ai-service/config')
export const saveAIServiceConfig = (input: AIServiceInput) => requestData<AIServiceStatus>('/api/v1/ai-service/config', { method: 'PUT', body: JSON.stringify(input) })
export const listAIModels = () => requestData<AIModel[]>('/api/v1/ai-service/models')
export const startAIRun = (message: string) => requestData<AIRun>('/api/v1/ai-service/runs', { method: 'POST', body: JSON.stringify({ message }) })
export async function startAIRunStream(message: string, history: AIConversationTurn[], signal: AbortSignal | undefined, onEvent: (event: AIRunStreamEvent) => void): Promise<void> {
  const response = await requestStream('/api/v1/ai-service/runs/stream', { method: 'POST', body: JSON.stringify({ message, history }), signal })
  if (!response.body) throw new Error('浏览器不支持流式响应')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let pending = ''
  const consume = (block: string) => {
    const data = block.split('\n').filter((line) => line.startsWith('data:')).map((line) => line.slice(5).trim()).join('\n')
    if (!data) return
    const event = JSON.parse(data) as AIRunStreamEvent
    onEvent(event)
  }
  for (;;) {
    const next = await reader.read()
    if (next.done) break
    pending += decoder.decode(next.value, { stream: true }).replace(/\r\n/g, '\n')
    let boundary = pending.indexOf('\n\n')
    while (boundary >= 0) {
      consume(pending.slice(0, boundary))
      pending = pending.slice(boundary + 2)
      boundary = pending.indexOf('\n\n')
    }
  }
  pending += decoder.decode()
  if (pending.trim()) consume(pending)
}
export const getAIRun = (id: string) => requestData<AIRun>(`/api/v1/ai-service/runs/${encodeURIComponent(id)}`)
export const approveAIRun = (id: string) => requestData<AIRun>(`/api/v1/ai-service/runs/${encodeURIComponent(id)}/approve`, { method: 'POST' })
export const listAIAutomations = () => requestData<AIAutomation[]>('/api/v1/ai-service/automations')
export const createAIAutomation = (input: AIAutomationInput) => requestData<AIAutomation>('/api/v1/ai-service/automations', { method: 'POST', body: JSON.stringify(input) })
export const saveAIAutomation = (id: string, input: AIAutomationInput) => requestData<AIAutomation>(`/api/v1/ai-service/automations/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(input) })
export const deleteAIAutomation = (id: string) => requestData<void>(`/api/v1/ai-service/automations/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const runAIAutomation = (id: string) => requestData<{ automation: AIAutomation, run: AIAutomationRun }>(`/api/v1/ai-service/automations/${encodeURIComponent(id)}/run`, { method: 'POST' })
export const approveAIAutomationRun = (automationID: string, runID: string) => requestData<{ automation: AIAutomation, run: AIAutomationRun }>(`/api/v1/ai-service/automations/${encodeURIComponent(automationID)}/runs/${encodeURIComponent(runID)}/approve`, { method: 'POST' })
