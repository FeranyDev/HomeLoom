import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { SystemDashboard } from './SystemDashboard'

describe('SystemDashboard', () => {
  it('shows queue health and command lifecycle', () => {
    render(<SystemDashboard diagnostics={{ eventsReceived: 12, eventsProcessed: 11, eventsDropped: 1, eventQueuePending: 2, eventQueueCapacity: 128, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 3, commandsStarted: 2, commandsConfirmed: 1, commandsRejected: 1, commandsTimedOut: 0, onlineDevices: 2, offlineDevices: 1, providersRunning: 1, providerRetries: 3, deviceSubscribers: 2, stateSubscribers: 1, commandAverageLatencyMs: 12.5 }} commands={[{ id: 'command-1', deviceId: 'switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', expected: { type: 'bool', bool: true }, status: 'rejected', error: 'offline', createdAt: '2026-07-13T00:00:00Z', updatedAt: '2026-07-13T00:00:01Z', deadline: '2026-07-13T00:00:05Z' }]} />)
    expect(screen.getByText('50%')).toBeInTheDocument(); expect(screen.getByText('switch-1')).toBeInTheDocument(); expect(screen.getByText('rejected')).toBeInTheDocument(); expect(screen.getByText('offline')).toBeInTheDocument()
  })
})
