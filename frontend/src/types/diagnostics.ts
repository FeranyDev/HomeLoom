export interface Diagnostics {
  eventsReceived: number; eventsProcessed: number; eventsDropped: number
  eventQueuePending: number; eventQueueCapacity: number; targetEventsDropped: number; stateEventsDropped: number
  statesMarkedStale: number; commandsStarted: number; commandsConfirmed: number
  commandsRejected: number; commandsTimedOut: number
  onlineDevices: number; offlineDevices: number; providersRunning: number; deviceSubscribers: number; stateSubscribers: number
  providerRetries: number
  commandAverageLatencyMs: number
}

export interface CommandValue { type: string; bool?: boolean; number?: number; string?: string }
export interface DeviceCommand {
  id: string; deviceId: string; endpointId: string; capabilityId: string; propertyId: string
  expected: CommandValue; status: 'queued' | 'sent' | 'accepted' | 'confirmed' | 'rejected' | 'timeout'
  error?: string; createdAt: string; updatedAt: string; deadline: string
}
