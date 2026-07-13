import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DeviceDetails } from './DeviceDetails'
import type { Device } from '../types/device'

afterEach(() => vi.unstubAllGlobals())

describe('DeviceDetails', () => {
  it('renders capability tree and state provenance', async () => {
    class StateSource { static current: StateSource; listeners = new Map<string, (event: { data: string }) => void>(); constructor() { StateSource.current = this } addEventListener(type: string, listener: (event: { data: string }) => void) { this.listeners.set(type, listener) } close() {} emit(type: string, value: unknown) { this.listeners.get(type)?.({ data: JSON.stringify(value) }) } }
    vi.stubGlobal('EventSource', StateSource)
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async (path: string) => ({ ok: true, json: async () => path.endsWith('/states') ? ({ data: [{ key: { deviceId: 'switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, value: { kind: 'bool', bool: true }, providerId: 'virtual-main', source: 'reported', observedAt: '2026-07-13T00:00:00Z', receivedAt: '2026-07-13T00:00:00Z', sequence: 7, version: 3, quality: 'reported' }] }) : ({ data: { definition: { id: 'power', name: '开关', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } } }) })))
    const device: Device = { schemaVersion: 1, id: 'switch-1', providerId: 'virtual-main', name: '客厅开关', type: 'switch', online: true, lastUpdateAt: '2026-07-13T00:00:00Z', endpoints: [{ id: 'main', name: '主端点', type: 'switch', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: '开关', type: 'bool', readable: true, writable: true, notifiable: true, staleAfterSeconds: 15 }, value: { type: 'bool', bool: true } }] }] }] }
    const onClose = vi.fn(); render(<DeviceDetails device={device} onClose={onClose} onPropertyWrite={vi.fn().mockResolvedValue(undefined)} onCommandExecute={vi.fn().mockResolvedValue(undefined)} />)
    expect(screen.getByText('CAPABILITY')).toBeInTheDocument(); expect(screen.getByText('RWN')).toBeInTheDocument(); expect(await screen.findByText('virtual-main · reported')).toBeInTheDocument(); expect(screen.getByText('3 / seq 7')).toBeInTheDocument()
    act(() => StateSource.current.emit('state', { key: { deviceId: 'switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, value: { kind: 'bool', bool: true }, providerId: 'virtual-main', source: 'reported', observedAt: '2026-07-13T00:00:00Z', receivedAt: '2026-07-13T00:00:02Z', sequence: 7, version: 4, quality: 'stale' })); expect(screen.getByText('stale')).toBeInTheDocument(); expect(screen.getByText('4 / seq 7')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '从 Provider 读取' })); expect(await screen.findByText('false')).toBeInTheDocument()
    await userEvent.keyboard('{Escape}'); expect(onClose).toHaveBeenCalledOnce()
  })
})
