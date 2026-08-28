import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { subscribeDeviceStates } from './devices'
import { subscribeEvents } from './events'

class FakeEventSource {
	static instances: FakeEventSource[] = []
	readonly listeners = new Map<string, Array<(event: MessageEvent<string>) => void>>()
	onerror: (() => void) | null = null
	closed = false

	constructor(readonly url: string) {
		FakeEventSource.instances.push(this)
	}

	addEventListener(name: string, handler: (event: MessageEvent<string>) => void) {
		this.listeners.set(name, [...(this.listeners.get(name) ?? []), handler])
	}

	emit(name: string, value: unknown) {
		const event = new MessageEvent('message', { data: JSON.stringify(value) })
		for (const handler of this.listeners.get(name) ?? []) handler(event)
	}

	close() {
		this.closed = true
	}
}

beforeEach(() => {
	FakeEventSource.instances = []
	vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => vi.unstubAllGlobals())

describe('unified event stream', () => {
	it('shares one connection and dispatches named changes to all subscribers', () => {
		const onConnection = vi.fn()
		const onDevice = vi.fn()
		const onRuntime = vi.fn()
		const onRuntimeLog = vi.fn()
		const onReady = vi.fn()
		const onRuntimeLogGap = vi.fn()
		const unsubscribeConnection = subscribeEvents({ onConnection, onDevice, onRuntimeLog, onReady, onRuntimeLogGap })
		const unsubscribeRuntime = subscribeEvents({ onRuntime })

		expect(FakeEventSource.instances).toHaveLength(1)
		expect(FakeEventSource.instances[0].url).toBe('/api/v1/events')
		FakeEventSource.instances[0].emit('ready', {})
		FakeEventSource.instances[0].emit('device', { id: 'light-1' })
		FakeEventSource.instances[0].emit('runtime', { diagnostics: { eventsProcessed: 3 } })
		FakeEventSource.instances[0].emit('runtime-log', { sequence: 7, process: 'backend', instance: 'main', message: 'ready' })
		FakeEventSource.instances[0].emit('runtime-log-gap', { after: 7, before: 14 })
		expect(onConnection).toHaveBeenCalledWith(true)
		expect(onReady).toHaveBeenCalledOnce()
		expect(onDevice).toHaveBeenCalledWith({ id: 'light-1', endpoints: [] })
		expect(onRuntime).toHaveBeenCalledWith({ diagnostics: { eventsProcessed: 3 } })
		expect(onRuntimeLog).toHaveBeenCalledWith({ sequence: 7, process: 'backend', instance: 'main', message: 'ready' })
		expect(onRuntimeLogGap).toHaveBeenCalledWith({ after: 7, before: 14 })

		unsubscribeConnection()
		expect(FakeEventSource.instances[0].closed).toBe(false)
		unsubscribeRuntime()
		expect(FakeEventSource.instances[0].closed).toBe(true)
	})

	it('normalizes null device collections before dispatching provider updates', () => {
		const handler = vi.fn()
		const unsubscribe = subscribeEvents({ onDevice: handler })
		FakeEventSource.instances[0].emit('device', { id: 'sonoff-1', endpoints: null })
		expect(handler).toHaveBeenCalledWith({ id: 'sonoff-1', endpoints: [] })
		unsubscribe()
	})

	it('filters unified state changes for the requested device', () => {
		const handler = vi.fn()
		const unsubscribe = subscribeDeviceStates('wanted', handler)
		const source = FakeEventSource.instances[0]
		source.emit('state', { key: { deviceId: 'other' } })
		source.emit('state', { key: { deviceId: 'wanted' }, quality: 'reported' })
		expect(handler).toHaveBeenCalledOnce()
		expect(handler).toHaveBeenCalledWith({ key: { deviceId: 'wanted' }, quality: 'reported' })
		unsubscribe()
	})

	it('normalizes flat Matter target events before dispatching them', () => {
		const handler = vi.fn()
		const unsubscribe = subscribeEvents({ onTarget: handler })
		const source = FakeEventSource.instances[0]

		source.emit('target', {
			id: 'matter-auto', type: 'matter', name: 'Matter', enabled: true, status: 'starting',
			commissioningState: 'uncommissioned', commissioningWindowOpen: false,
			networkInterface: 'en0', udpPort: 5540, fabricCount: 0, endpointCount: 0,
		})

		expect(handler).toHaveBeenCalledWith(expect.objectContaining({
			id: 'matter-auto',
			type: 'matter',
			config: expect.objectContaining({ networkInterface: 'en0', udpPort: 5540 }),
			commissioning: { state: 'uncommissioned', windowOpen: false, windowExpiresAt: undefined, manualPairingCode: undefined, setupPayload: undefined },
		}))
		expect(handler.mock.calls[0][0]).not.toHaveProperty('pairing')
		unsubscribe()
	})
})
