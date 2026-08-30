import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMappingProfile } from './mapping'

afterEach(() => vi.unstubAllGlobals())

describe('mapping profile API', () => {
  it('does not send a client-side profile ID when creating a profile', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { id: '018cc251-f400-7000-8000-000000000001' } }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await createMappingProfile({
      schemaVersion: 1,
      id: 'temporary-client-id',
      identifier: 'fan-mode',
      version: 1,
      kind: 'provider',
      inputType: 'enum',
      outputType: 'enum',
      transforms: [{ type: 'enum', values: { auto: 'auto' } }],
    })

    expect(JSON.parse(fetchMock.mock.calls[0][1].body as string)).toEqual({
      schemaVersion: 1,
      identifier: 'fan-mode',
      version: 1,
      kind: 'provider',
      inputType: 'enum',
      outputType: 'enum',
      transforms: [{ type: 'enum', values: { auto: 'auto' } }],
    })
  })
})
