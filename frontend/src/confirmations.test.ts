import { afterEach, describe, expect, it, vi } from 'vitest'
import { confirmProviderDeletion, confirmTargetDeletion } from './confirmations'

afterEach(() => vi.restoreAllMocks())

describe('destructive confirmations', () => {
  it('requires explicit confirmation and describes the impact', () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    expect(confirmTargetDeletion('Main')).toBe(false)
    expect(confirmProviderDeletion('Virtual')).toBe(true)
    expect(confirm.mock.calls[0][0]).toContain('配对资料目录')
    expect(confirm.mock.calls[1][0]).toContain('立即离线')
  })
})
