import { afterEach, describe, expect, it, vi } from 'vitest'
import { listLogicalDeviceCandidates, saveLogicalDevice } from './logicalDevices'

afterEach(() => vi.unstubAllGlobals())

describe('Logical Device API', () => {
  it('uses the explicit candidates endpoint and persists a manual binding', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { id: 'living-switch' } }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await listLogicalDeviceCandidates()
    await saveLogicalDevice({ id: 'living-switch', name: '客厅主灯', type: 'switch', bindings: [{ providerId: 'local', deviceId: 'light-1', priority: 0 }, { providerId: 'cloud', deviceId: 'light-1', priority: 10 }] }, false)
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/v1/logical-devices/candidates', expect.any(Object))
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/logical-devices', expect.objectContaining({ method: 'POST' }))
  })
})
