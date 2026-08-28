import { afterEach, describe, expect, it, vi } from 'vitest'
import { listDevices, resetDeviceName, setDeviceName, setDevicePower } from './devices'

afterEach(() => vi.unstubAllGlobals())

describe('device API normalization', () => {
	it('normalizes null endpoint collections from the device list', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: [{ id: 'sonoff-1', endpoints: null }] }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
		await expect(listDevices()).resolves.toEqual([{ id: 'sonoff-1', endpoints: [] }])
	})

	it('normalizes null endpoint collections from device mutations', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { id: 'sonoff-1', endpoints: null } }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
		await expect(setDevicePower('sonoff-1', true)).resolves.toEqual({ id: 'sonoff-1', endpoints: [] })
	})

	it('uses the unified name endpoint for every provider device', async () => {
		const response = () => new Response(JSON.stringify({ data: { id: 'tuya-switch', name: '玄关开关', endpoints: null } }), { status: 200, headers: { 'Content-Type': 'application/json' } })
		const fetch = vi.fn().mockResolvedValueOnce(response()).mockResolvedValueOnce(response())
		vi.stubGlobal('fetch', fetch)
		await expect(setDeviceName('tuya-switch', '玄关开关')).resolves.toEqual({ id: 'tuya-switch', name: '玄关开关', endpoints: [] })
		expect(fetch).toHaveBeenCalledWith('/api/v1/devices/tuya-switch/name', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ name: '玄关开关' }) }))
		await resetDeviceName('tuya-switch')
		expect(fetch).toHaveBeenLastCalledWith('/api/v1/devices/tuya-switch/name', expect.objectContaining({ method: 'DELETE' }))
	})
})
