export interface Diagnostics {
  eventsReceived: number; eventsProcessed: number; eventsDropped: number
  eventQueuePending: number; eventQueueCapacity: number; targetEventsDropped: number; stateEventsDropped: number
  statesMarkedStale: number; commandsStarted: number; commandsConfirmed: number
  commandsRejected: number; commandsTimedOut: number
  commandsSuperseded: number
  onlineDevices: number; offlineDevices: number; providersRunning: number; deviceSubscribers: number; stateSubscribers: number
  providerRetries: number
  commandAverageLatencyMs: number
	// Runtime metrics are sampled when diagnostics are requested.
	goroutines: number; heapAllocBytes: number; heapObjects: number
}

export interface SystemVersion { version: string; commit: string; buildTime: string; goVersion: string }

export interface CommandValue { type: string; bool?: boolean; number?: number; string?: string }
export interface DeviceCommand {
  id: string; deviceId: string; endpointId: string; capabilityId: string; propertyId: string
  expected: CommandValue; status: 'queued' | 'sent' | 'accepted' | 'confirmed' | 'rejected' | 'timeout' | 'superseded'
  error?: string; createdAt: string; updatedAt: string; deadline: string
}
