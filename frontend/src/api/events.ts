import type { AuditEvent, DeviceCommand, Diagnostics } from '../types/diagnostics'
import type { Device, StateValue } from '../types/device'
import type { Provider } from '../types/provider'
import type { Target } from '../types/target'

export interface RuntimeDelta {
	providers?: Provider[]
	diagnostics?: Diagnostics
}

export interface EventHandlers {
	onConnection?: (connected: boolean) => void
	onDevice?: (device: Device) => void
	onState?: (state: StateValue) => void
	onCommand?: (command: DeviceCommand) => void
	onAudit?: (event: AuditEvent) => void
	onTarget?: (target: Target) => void
	onRuntime?: (delta: RuntimeDelta) => void
}

const subscriptions = new Set<EventHandlers>()
let source: EventSource | null = null

function dispatch<K extends keyof EventHandlers>(name: K, value: Parameters<NonNullable<EventHandlers[K]>>[0]) {
	for (const handlers of subscriptions) {
		const handler = handlers[name]
		if (handler) (handler as (current: typeof value) => void)(value)
	}
}

function parse<T>(event: Event): T | undefined {
	try { return JSON.parse((event as MessageEvent<string>).data) as T } catch { return undefined }
}

function ensureSource() {
	if (source) return
	source = new EventSource('/api/v1/events')
	source.addEventListener('ready', () => dispatch('onConnection', true))
	source.addEventListener('device', (event) => { const value = parse<Device>(event); if (value) dispatch('onDevice', value) })
	source.addEventListener('state', (event) => { const value = parse<StateValue>(event); if (value) dispatch('onState', value) })
	source.addEventListener('command', (event) => { const value = parse<DeviceCommand>(event); if (value) dispatch('onCommand', value) })
	source.addEventListener('audit', (event) => { const value = parse<AuditEvent>(event); if (value) dispatch('onAudit', value) })
	source.addEventListener('target', (event) => { const value = parse<Target>(event); if (value) dispatch('onTarget', value) })
	source.addEventListener('runtime', (event) => { const value = parse<RuntimeDelta>(event); if (value) dispatch('onRuntime', value) })
	source.onerror = () => dispatch('onConnection', false)
}

export function subscribeEvents(handlers: EventHandlers): () => void {
	subscriptions.add(handlers)
	ensureSource()
	return () => {
		subscriptions.delete(handlers)
		if (subscriptions.size === 0 && source) {
			source.close()
			source = null
		}
	}
}
