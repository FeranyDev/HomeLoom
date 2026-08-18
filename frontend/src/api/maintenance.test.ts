import { afterEach, describe, expect, it, vi } from 'vitest'
import { getMasterKeyStatus, rotateMasterKey } from './maintenance'

afterEach(() => vi.restoreAllMocks())

describe('master key maintenance API', () => {
	it('reads opaque key status without sending credentials', async () => {
		const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { activeVersion: 2, retainedVersions: [1, 2], ciphertextsByVersion: { 2: 4 }, legacyCiphertexts: 0, needsReencryption: false } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
		vi.stubGlobal('fetch', fetchMock)
		await expect(getMasterKeyStatus()).resolves.toMatchObject({ activeVersion: 2, retainedVersions: [1, 2] })
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/system/master-key', expect.objectContaining({ credentials: 'same-origin' }))
	})

	it('sends the explicit confirmation and resume flag only', async () => {
		const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { previousVersion: 1, activeVersion: 2, reencrypted: 4, status: { activeVersion: 2, retainedVersions: [1, 2], ciphertextsByVersion: { 2: 4 }, legacyCiphertexts: 0, needsReencryption: false } } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
		vi.stubGlobal('fetch', fetchMock)
		await expect(rotateMasterKey('ROTATE', true)).resolves.toMatchObject({ activeVersion: 2, reencrypted: 4 })
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/system/master-key/rotate', expect.objectContaining({ method: 'POST', body: JSON.stringify({ confirmation: 'ROTATE', resume: true }) }))
	})
})
