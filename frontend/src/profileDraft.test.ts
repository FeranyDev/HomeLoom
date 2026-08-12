import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildProfileDraft, consumeProfileDraft, openProfileWorkbench, storeProfileDraft } from './profileDraft'

afterEach(() => {
  window.sessionStorage.clear()
  window.localStorage.clear()
  window.history.replaceState(null, '', '/')
  vi.restoreAllMocks()
})

describe('profileDraft', () => {
  it('builds an enum mapping draft from mismatched domains', () => {
    const draft = buildProfileDraft({
      stage: 'provider',
      inputType: 'enum',
      outputType: 'enum',
      sourceEnum: ['Automatic', 'Silent'],
      targetEnum: ['auto', 'low', 'high'],
      sourceLabel: 'fan-level',
      targetLabel: 'fan-speed',
    })
    expect(draft.kind).toBe('provider')
    expect(draft.inputType).toBe('enum')
    expect(draft.outputType).toBe('enum')
    expect(draft.transforms[0]).toEqual({
      type: 'enum',
      values: { Automatic: 'auto', Silent: 'low' },
    })
    expect(draft.id).toContain('provider')
  })

  it('stores and consumes a one-shot draft for the profile workbench', () => {
    storeProfileDraft({ stage: 'consumer', inputType: 'number', outputType: 'int', sourceLabel: 'brightness', targetLabel: 'level' })
    const draft = consumeProfileDraft()
    expect(draft?.inputType).toBe('number')
    expect(draft?.outputType).toBe('int')
    expect(consumeProfileDraft()).toBeNull()
  })

  it('opens a separate profile workbench tab with a one-shot prefilled draft', () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    openProfileWorkbench({ stage: 'provider', inputType: 'bool', outputType: 'bool' })
    const target = open.mock.calls[0][0]!
    expect(target).toMatch(/^\?profile-draft=.+#\/mapping\/profiles$/)
    window.history.replaceState(null, '', target)
    expect(consumeProfileDraft()?.transforms[0]).toEqual({ type: 'invert' })
    expect(window.location.search).toBe('')
    expect(consumeProfileDraft()).toBeNull()
  })
})
