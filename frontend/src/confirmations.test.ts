import { afterEach, describe, expect, it, vi } from 'vitest'
import { confirmExactPhrase, confirmProviderDeletion, confirmTargetDeletion } from './confirmations'

afterEach(() => vi.restoreAllMocks())

describe('destructive confirmations', () => {
  it('requires explicit confirmation and describes the impact', () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    expect(confirmTargetDeletion('Main')).toBe(false)
    expect(confirmProviderDeletion('Virtual')).toBe(true)
    expect(confirm.mock.calls[0][0]).toContain('配对资料目录')
    expect(confirm.mock.calls[1][0]).toContain('立即离线')
  })

	it('requires the complete high-risk confirmation phrase', () => {
		const prompt = vi.spyOn(window, 'prompt').mockReturnValueOnce('CLEAR').mockReturnValueOnce('CLEAR apple-main')
		expect(confirmExactPhrase('清除身份', 'CLEAR apple-main')).toBeNull()
		expect(confirmExactPhrase('清除身份', 'CLEAR apple-main')).toBe('CLEAR apple-main')
		expect(prompt.mock.calls[0][0]).toContain('CLEAR apple-main')
	})
})
