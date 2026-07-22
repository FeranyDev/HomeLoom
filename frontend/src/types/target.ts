export type TargetType = 'apple-hap' | 'matter'
export type TargetStatus = 'disabled' | 'starting' | 'running' | 'error'

export interface TargetVirtualDevice {
	id: string
	name: string
	type: import('./device').DeviceType
	sourceDeviceId: string
	auxiliarySourceDeviceIds?: string[]
	enabled: boolean
}

export interface Target {
  id: string
  type: TargetType
  consumerId?: string
  name: string
  enabled: boolean
  status: TargetStatus
  address?: string
	setupId?: string
  pairingCode?: string
  setupUri?: string
  deviceIds: string[]
	devices: TargetVirtualDevice[]
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
	devices: TargetVirtualDevice[]
}
