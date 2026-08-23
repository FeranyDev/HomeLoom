export interface AIServiceStatus {
  apiBaseUrl: string
  apiProxyUrl: string
  model: string
  apiProtocol: AIAPIProtocol
  agentInstructions: string
  defaultAgentInstructions: string
  apiKeyConfigured: boolean
  configured: boolean
}

export type AIAPIProtocol = 'responses' | 'chat-completions'

export interface AIServiceInput {
  apiBaseUrl: string
  apiProxyUrl: string
  apiKey?: string
  model: string
  apiProtocol: AIAPIProtocol
  agentInstructions: string
}

export interface AIModel { id: string }

export type AIRunStatus = 'completed' | 'awaiting_approval' | 'executed' | 'failed'
export interface AIRunAction {
  deviceId: string
  endpointId: string
  capabilityId: string
  propertyId: string
  value: PropertyValue
  expectedStateVersion?: number
  deviceName: string
  propertyName: string
  usageNote?: string
}
export interface AIRun {
  id: string
  status: AIRunStatus
  message: string
  createdAt: string
  expiresAt?: string
  action?: AIRunAction
}

export type AIAutomationKind = 'schedule' | 'trigger'
export type AIAutomationExecutionMode = 'unattended' | 'manual'
export interface AIAutomationTrigger {
  deviceId: string
  endpointId: string
  capabilityId: string
  propertyId: string
  value: PropertyValue
}
export interface AIAutomation {
  id: string
  name: string
  enabled: boolean
  kind: AIAutomationKind
  prompt: string
  executionMode: AIAutomationExecutionMode
  intervalSeconds?: number
  cooldownSeconds?: number
  trigger?: AIAutomationTrigger
  lastRunId?: string
  lastRunStatus?: string
  lastRunMessage?: string
  lastRunAt?: string
  runHistory?: AIAutomationRun[]
  createdAt: string
  updatedAt: string
}
export type AIAutomationInput = Omit<AIAutomation, 'id' | 'lastRunId' | 'lastRunStatus' | 'lastRunMessage' | 'lastRunAt' | 'runHistory' | 'createdAt' | 'updatedAt'>
export interface AIAutomationRun extends AIRun {
  source?: 'manual' | 'schedule' | 'trigger'
  autoApproved?: boolean
}
import type { PropertyValue } from './device'
