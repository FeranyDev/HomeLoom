export type TargetType = 'apple-hap' | 'matter'
export type TargetStatus = 'disabled' | 'starting' | 'running' | 'error'

export interface Target {
  id: string
  type: TargetType
  name: string
  enabled: boolean
  status: TargetStatus
  address?: string
	setupId?: string
  pairingCode?: string
  setupUri?: string
  deviceIds: string[]
}

export interface TargetInput {
	id: string
	type: TargetType
	name: string
	enabled: boolean
	address: string
	pin: string
	setupId: string
	deviceIds: string[]
}
