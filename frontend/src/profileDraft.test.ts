import { afterEach, describe, expect, it, vi } from 'vitest'
import { buildProfileDraft, consumeProfileDraft, openProfileWorkbench, storeProfileDraft } from './profileDraft'

afterEach(() => {
  sessionStorage.clear()
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

  it('opens the profile workbench with a prefilled draft', () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    openProfileWorkbench({ stage: 'provider', inputType: 'bool', outputType: 'bool' })
    expect(open).toHaveBeenCalledWith('#/mapping/profiles', '_blank', 'noopener,noreferrer')
    expect(consumeProfileDraft()?.transforms[0]).toEqual({ type: 'invert' })
  })
})
