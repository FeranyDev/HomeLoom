export type DeviceType = 'switch' | 'temperature-sensor'

export interface DeviceState {
  power?: boolean
  temperature?: number
}

export interface Device {
  id: string
  providerId: string
  name: string
  type: DeviceType
  online: boolean
  state: DeviceState
  lastUpdateAt: string
}

