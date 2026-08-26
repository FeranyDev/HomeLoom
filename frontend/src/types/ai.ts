export interface AIServiceStatus {
  apiBaseUrl: string
  apiProxyUrl: string
  model: string
  apiProtocol: AIAPIProtocol
  agentInstructions: string
  defaultAgentInstructions: string
  sessionContext: AISessionContextSettings
  homePreferences: AIHomePreferences
  apiKeyConfigured: boolean
  configured: boolean
}

export type AIAPIProtocol = 'responses' | 'chat-completions'

export interface AISessionContextSettings {
  enabled: boolean
  currentTime: boolean
  timeZone: boolean
  weekday: boolean
  runSource: boolean
  triggerState: boolean
  regionLanguage: boolean
  temperatureUnit: boolean
}

export type AITemperatureUnit = 'celsius' | 'fahrenheit'
export interface AIHomePreferences {
  timeZone: string
  regionLanguage: string
  temperatureUnit: AITemperatureUnit
}

export interface AIServiceInput {
  apiBaseUrl: string
  apiProxyUrl: string
  apiKey?: string
  clearApiKey?: boolean
  model: string
  apiProtocol: AIAPIProtocol
  agentInstructions: string
  sessionContext: AISessionContextSettings
  homePreferences: AIHomePreferences
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
export interface AIConversationTurn { role: 'user' | 'assistant'; content: string }
export interface AIRunStreamEvent { type: 'ready' | 'delta' | 'run' | 'error'; delta?: string; run?: AIRun; error?: string }

export type AIAutomationKind = 'schedule' | 'trigger'
export type AIAutomationExecutionMode = 'unattended' | 'manual'
export type AIAutomationConditionMatch = 'all' | 'any'
export interface AIAutomationTrigger {
  deviceId: string
  endpointId: string
  capabilityId: string
  propertyId: string
  value: PropertyValue
}
export type AIAutomationConditionOperator = 'equals' | 'not_equals' | 'greater_than' | 'greater_than_or_equal' | 'less_than' | 'less_than_or_equal'
export interface AIAutomationCondition {
  deviceId: string
  endpointId: string
  capabilityId: string
  propertyId: string
  operator: AIAutomationConditionOperator
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
  cronExpression?: string
  cooldownSeconds?: number
  trigger?: AIAutomationTrigger
  conditions?: AIAutomationCondition[]
  conditionMatch?: AIAutomationConditionMatch
  generation: number
  lastRunId?: string
  lastRunStatus?: string
  lastRunMessage?: string
  lastRunAt?: string
  runHistory?: AIAutomationRun[]
  createdAt: string
  updatedAt: string
}
export type AIAutomationInput = Omit<AIAutomation, 'id' | 'generation' | 'lastRunId' | 'lastRunStatus' | 'lastRunMessage' | 'lastRunAt' | 'runHistory' | 'createdAt' | 'updatedAt'>
export interface AIAutomationRun extends AIRun {
  source?: 'manual' | 'schedule' | 'trigger'
  autoApproved?: boolean
  automationGeneration?: number
}
import type { PropertyValue } from './device'
