import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { SystemDashboard } from './SystemDashboard'
import type { DeviceCommand } from '../types/diagnostics'

describe('SystemDashboard', () => {
	it('offers explicitly redacted support downloads', () => {
		const diagnostics = { eventsReceived: 0, eventsProcessed: 0, eventsDropped: 0, eventQueuePending: 0, eventQueueCapacity: 1, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 0, commandsStarted: 0, commandsConfirmed: 0, commandsRejected: 0, commandsTimedOut: 0, commandsSuperseded: 0, commandsOutcomeUnknown: 0, homeKitPushes: 0, onlineDevices: 0, offlineDevices: 0, unknownDevices: 0, providersRunning: 0, providerRetries: 0, deviceSubscribers: 0, stateSubscribers: 0, commandAverageLatencyMs: 0, commandQueuePending: 0, commandQueueMaxPending: 0, eventAverageLatencyMs: 0, eventMaxLatencyMs: 0, slowEventHandlers: 0, databaseOperations: 0, databaseAverageLatencyMs: 0, databaseMaxLatencyMs: 0, providerClockSkewEvents: 0, providerMaxClockSkewMs: 0, goroutines: 1, heapAllocBytes: 0, heapObjects: 0 }
		render(<SystemDashboard diagnostics={diagnostics} commands={[]} settings={{ commandTimeoutSeconds: 5, commandHistoryLimit: 1000 }} onSettingsSave={vi.fn()} />)
		expect(screen.getByRole('link', { name: '导出脱敏配置' })).toHaveAttribute('href', '/api/v1/system/config-export')
		expect(screen.getByRole('link', { name: '下载诊断包' })).toHaveAttribute('href', '/api/v1/system/diagnostic-bundle')
		expect(screen.getByText('已自动脱敏')).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '下载完整备份' })).toBeInTheDocument()
		expect(screen.getByLabelText('恢复压缩包')).toBeInTheDocument()
	})
	it('shows persistent audit events and correlation IDs', () => {
		const diagnostics = { eventsReceived: 0, eventsProcessed: 0, eventsDropped: 0, eventQueuePending: 0, eventQueueCapacity: 1, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 0, commandsStarted: 0, commandsConfirmed: 0, commandsRejected: 0, commandsTimedOut: 0, commandsSuperseded: 0, commandsOutcomeUnknown: 0, homeKitPushes: 0, onlineDevices: 0, offlineDevices: 0, unknownDevices: 0, providersRunning: 0, providerRetries: 0, deviceSubscribers: 0, stateSubscribers: 0, commandAverageLatencyMs: 0, commandQueuePending: 0, commandQueueMaxPending: 0, eventAverageLatencyMs: 0, eventMaxLatencyMs: 0, slowEventHandlers: 0, databaseOperations: 0, databaseAverageLatencyMs: 0, databaseMaxLatencyMs: 0, providerClockSkewEvents: 0, providerMaxClockSkewMs: 0, goroutines: 1, heapAllocBytes: 0, heapObjects: 0 }
		render(<SystemDashboard diagnostics={diagnostics} commands={[]} auditEvents={[{ id: 1, correlationId: 'trace-123', actor: 'local-api', action: 'restart', resourceType: 'provider', resourceId: 'virtual-main', method: 'POST', route: '/api/v1/providers/:id/restart', status: 200, outcome: 'succeeded', createdAt: '2026-07-13T00:00:00Z' }]} settings={{ commandTimeoutSeconds: 5, commandHistoryLimit: 1000 }} onSettingsSave={vi.fn()} />)
		expect(screen.getByText('实时审计日志')).toBeInTheDocument()
		expect(screen.getByText('trace-123')).toBeInTheDocument()
		expect(screen.getByText('provider · virtual-main')).toBeInTheDocument()
	})
  it('shows queue health and command lifecycle', () => {
    render(<SystemDashboard diagnostics={{ eventsReceived: 12, eventsProcessed: 11, eventsDropped: 1, eventQueuePending: 2, eventQueueCapacity: 128, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 3, commandsStarted: 2, commandsConfirmed: 1, commandsRejected: 1, commandsTimedOut: 0, commandsSuperseded: 0, commandsCoalesced: 1, commandsOutcomeUnknown: 1, homeKitPushes: 8, onlineDevices: 2, offlineDevices: 1, unknownDevices: 0, providersRunning: 1, providerRetries: 3, deviceSubscribers: 2, stateSubscribers: 1, commandAverageLatencyMs: 12.5, commandQueuePending: 0, commandQueueMaxPending: 2, eventAverageLatencyMs: 1.2, eventMaxLatencyMs: 4.8, slowEventHandlers: 1, databaseOperations: 14, databaseAverageLatencyMs: 0.6, databaseMaxLatencyMs: 2.4, providerClockSkewEvents: 1, providerMaxClockSkewMs: 600000, goroutines: 21, heapAllocBytes: 1572864, heapObjects: 900 }} commands={[{ id: 'command-1', deviceId: 'switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', expected: { type: 'bool', bool: true }, status: 'rejected', outcome: 'failed', coalescedRequests: 1, error: 'offline', createdAt: '2026-07-13T00:00:00Z', updatedAt: '2026-07-13T00:00:01Z', deadline: '2026-07-13T00:00:05Z' }]} settings={{ commandTimeoutSeconds: 5, commandHistoryLimit: 1000 }} onSettingsSave={vi.fn()} />)
    expect(screen.getByText('50%')).toBeInTheDocument(); expect(screen.getByText('1.5MB')).toBeInTheDocument(); expect(screen.getByText('4.8ms')).toBeInTheDocument(); expect(screen.getByText('14')).toBeInTheDocument(); expect(screen.getByText('8')).toBeInTheDocument(); expect(screen.getByText('switch-1')).toBeInTheDocument(); expect(screen.getAllByText('rejected')).toHaveLength(2); expect(screen.getByText('outcome: failed')).toBeInTheDocument(); expect(screen.getByText('offline')).toBeInTheDocument(); expect(screen.getByText('合并重复请求 × 1')).toBeInTheDocument()
  })

	it('filters command history by text and status', async () => {
		const diagnostics = { eventsReceived: 0, eventsProcessed: 0, eventsDropped: 0, eventQueuePending: 0, eventQueueCapacity: 1, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 0, commandsStarted: 0, commandsConfirmed: 0, commandsRejected: 0, commandsTimedOut: 0, commandsSuperseded: 0, commandsOutcomeUnknown: 0, homeKitPushes: 0, onlineDevices: 0, offlineDevices: 0, unknownDevices: 0, providersRunning: 0, providerRetries: 0, deviceSubscribers: 0, stateSubscribers: 0, commandAverageLatencyMs: 0, commandQueuePending: 0, commandQueueMaxPending: 0, eventAverageLatencyMs: 0, eventMaxLatencyMs: 0, slowEventHandlers: 0, databaseOperations: 0, databaseAverageLatencyMs: 0, databaseMaxLatencyMs: 0, providerClockSkewEvents: 0, providerMaxClockSkewMs: 0, goroutines: 1, heapAllocBytes: 0, heapObjects: 0 }
		const commands: DeviceCommand[] = [{ id: 'one', deviceId: 'kitchen', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', status: 'confirmed', createdAt: '2026-07-13T00:00:00Z', updatedAt: '2026-07-13T00:00:00Z', deadline: '2026-07-13T00:00:05Z' }, { id: 'two', deviceId: 'bedroom', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', status: 'rejected', error: 'offline', createdAt: '2026-07-13T00:00:00Z', updatedAt: '2026-07-13T00:00:00Z', deadline: '2026-07-13T00:00:05Z' }]
		render(<SystemDashboard diagnostics={diagnostics} commands={commands} settings={{ commandTimeoutSeconds: 5, commandHistoryLimit: 1000 }} onSettingsSave={vi.fn()} />)
		await userEvent.type(screen.getByLabelText('搜索命令'), 'kitchen'); expect(screen.getByText('kitchen')).toBeInTheDocument(); expect(screen.queryByText('bedroom')).not.toBeInTheDocument()
		await userEvent.clear(screen.getByLabelText('搜索命令')); await userEvent.selectOptions(screen.getByLabelText('命令状态'), 'rejected'); expect(screen.getByText('bedroom')).toBeInTheDocument(); expect(screen.queryByText('kitchen')).not.toBeInTheDocument()
	})

  it('saves command timeout settings', async () => {
	const onSave = vi.fn().mockResolvedValue(undefined)
	render(<SystemDashboard diagnostics={{ eventsReceived: 0, eventsProcessed: 0, eventsDropped: 0, eventQueuePending: 0, eventQueueCapacity: 1, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 0, commandsStarted: 0, commandsConfirmed: 0, commandsRejected: 0, commandsTimedOut: 0, commandsSuperseded: 0, commandsOutcomeUnknown: 0, homeKitPushes: 0, onlineDevices: 0, offlineDevices: 0, unknownDevices: 0, providersRunning: 0, providerRetries: 0, deviceSubscribers: 0, stateSubscribers: 0, commandAverageLatencyMs: 0, commandQueuePending: 0, commandQueueMaxPending: 0, eventAverageLatencyMs: 0, eventMaxLatencyMs: 0, slowEventHandlers: 0, databaseOperations: 0, databaseAverageLatencyMs: 0, databaseMaxLatencyMs: 0, providerClockSkewEvents: 0, providerMaxClockSkewMs: 0, goroutines: 1, heapAllocBytes: 0, heapObjects: 0 }} commands={[]} settings={{ commandTimeoutSeconds: 5, commandHistoryLimit: 1000 }} onSettingsSave={onSave} />)
	const input = screen.getByLabelText('命令确认超时秒数'); await userEvent.clear(input); await userEvent.type(input, '12'); await userEvent.click(screen.getByRole('button', { name: '保存并实时应用' }))
	expect(onSave).toHaveBeenCalledWith({ commandTimeoutSeconds: 12, commandHistoryLimit: 1000 })
  })

	it('paginates audit events and command history', async () => {
		const diagnostics = { eventsReceived: 0, eventsProcessed: 0, eventsDropped: 0, eventQueuePending: 0, eventQueueCapacity: 1, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 0, commandsStarted: 0, commandsConfirmed: 0, commandsRejected: 0, commandsTimedOut: 0, commandsSuperseded: 0, commandsOutcomeUnknown: 0, homeKitPushes: 0, onlineDevices: 0, offlineDevices: 0, unknownDevices: 0, providersRunning: 0, providerRetries: 0, deviceSubscribers: 0, stateSubscribers: 0, commandAverageLatencyMs: 0, commandQueuePending: 0, commandQueueMaxPending: 0, eventAverageLatencyMs: 0, eventMaxLatencyMs: 0, slowEventHandlers: 0, databaseOperations: 0, databaseAverageLatencyMs: 0, databaseMaxLatencyMs: 0, providerClockSkewEvents: 0, providerMaxClockSkewMs: 0, goroutines: 1, heapAllocBytes: 0, heapObjects: 0 }
		const auditEvents = Array.from({ length: 25 }, (_, index) => ({
			id: index + 1,
			correlationId: `trace-${index + 1}`,
			actor: 'local-api',
			action: 'update',
			resourceType: 'provider',
			resourceId: `provider-${index + 1}`,
			method: 'POST',
			route: '/api/v1/providers/:id',
			status: 200,
			outcome: 'succeeded' as const,
			createdAt: `2026-07-13T00:00:${String(index).padStart(2, '0')}Z`,
		}))
		const commands = Array.from({ length: 25 }, (_, index) => ({
			id: `command-${index + 1}`,
			deviceId: `switch-${index + 1}`,
			endpointId: 'main',
			capabilityId: 'switch',
			propertyId: 'power',
			expected: { type: 'bool' as const, bool: true },
			status: 'confirmed' as const,
			outcome: 'succeeded' as const,
			createdAt: `2026-07-13T00:00:${String(index).padStart(2, '0')}Z`,
			updatedAt: `2026-07-13T00:00:${String(index).padStart(2, '0')}Z`,
			deadline: `2026-07-13T00:00:05Z`,
		}))

		render(<SystemDashboard diagnostics={diagnostics} commands={commands} auditEvents={auditEvents} settings={{ commandTimeoutSeconds: 5, commandHistoryLimit: 1000 }} onSettingsSave={vi.fn()} />)

		expect(screen.getByText('provider · provider-1')).toBeInTheDocument()
		expect(screen.getByText('provider · provider-20')).toBeInTheDocument()
		expect(screen.queryByText('provider · provider-21')).not.toBeInTheDocument()
		expect(screen.getByText('switch-1')).toBeInTheDocument()
		expect(screen.getByText('switch-20')).toBeInTheDocument()
		expect(screen.queryByText('switch-21')).not.toBeInTheDocument()
		expect(screen.getAllByText('第 1–20 条 / 共 25 条 · 第 1 / 2 页')).toHaveLength(2)

		const nextButtons = screen.getAllByRole('button', { name: '下一页' })
		await userEvent.click(nextButtons[0])
		expect(screen.getByText('provider · provider-21')).toBeInTheDocument()
		expect(screen.getByText('provider · provider-25')).toBeInTheDocument()
		expect(screen.queryByText('provider · provider-1')).not.toBeInTheDocument()
		expect(screen.getByText('第 21–25 条 / 共 25 条 · 第 2 / 2 页')).toBeInTheDocument()

		await userEvent.click(nextButtons[1])
		expect(screen.getByText('switch-21')).toBeInTheDocument()
		expect(screen.getByText('switch-25')).toBeInTheDocument()
		expect(screen.queryByText('switch-1')).not.toBeInTheDocument()
		expect(screen.getAllByText('第 21–25 条 / 共 25 条 · 第 2 / 2 页')).toHaveLength(2)

		await userEvent.selectOptions(screen.getByLabelText('审计日志每页条数'), '10')
		expect(screen.getByText('provider · provider-1')).toBeInTheDocument()
		expect(screen.getByText('provider · provider-10')).toBeInTheDocument()
		expect(screen.queryByText('provider · provider-11')).not.toBeInTheDocument()
		expect(screen.getByText('第 1–10 条 / 共 25 条 · 第 1 / 3 页')).toBeInTheDocument()
	})
})
