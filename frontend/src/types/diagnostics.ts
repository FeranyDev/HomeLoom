export interface Diagnostics {
  eventsReceived: number; eventsProcessed: number; eventsDropped: number
  eventQueuePending: number; eventQueueCapacity: number; targetEventsDropped: number; stateEventsDropped: number
  statesMarkedStale: number; commandsStarted: number; commandsConfirmed: number
  commandsRejected: number; commandsTimedOut: number
  commandsSuperseded: number
	commandsCoalesced?: number
	commandsOutcomeUnknown: number
	homeKitPushes: number
  onlineDevices: number; offlineDevices: number; unknownDevices: number; providersRunning: number; deviceSubscribers: number; stateSubscribers: number
	disabledDevices?: number; removedDevices?: number
  providerRetries: number
  commandAverageLatencyMs: number
	commandQueuePending: number; commandQueueMaxPending: number
	eventAverageLatencyMs: number; eventMaxLatencyMs: number; slowEventHandlers: number
	databaseOperations: number; databaseAverageLatencyMs: number; databaseMaxLatencyMs: number
	providerClockSkewEvents: number; providerMaxClockSkewMs: number
	providerEventsIgnored?: number
	// Runtime metrics are sampled when diagnostics are requested.
	goroutines: number; heapAllocBytes: number; heapObjects: number
}

export interface SystemVersion { version: string; commit: string; buildTime: string; goVersion: string }
export interface RuntimeSettings { commandTimeoutSeconds: number; commandHistoryLimit: number }

export interface CommandValue { type: string; bool?: boolean; int?: number; number?: number; string?: string }
export interface DeviceCommand {
  id: string; kind?: 'property' | 'action'; deviceId: string; endpointId: string; capabilityId: string; propertyId?: string; commandId?: string
  expected?: CommandValue; parameters?: Record<string, CommandValue>; status: 'queued' | 'sent' | 'accepted' | 'confirmed' | 'rejected' | 'timeout' | 'superseded'
	outcome?: 'succeeded' | 'failed' | 'unknown'
	idempotencyKey?: string
	correlationId?: string
	coalescedRequests?: number
  error?: string; createdAt: string; updatedAt: string; deadline: string
}

export interface AuditEvent {
	id: number
	correlationId: string
	actor: string
	action: string
	resourceType: string
	resourceId?: string
	method: string
	route: string
	status: number
	outcome: 'succeeded' | 'failed'
	createdAt: string
}
