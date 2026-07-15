import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { usePageRoute } from './routing'

afterEach(() => { window.location.hash = '' })

describe('usePageRoute', () => {
  it('restores deep links and reacts to hash history', () => {
    window.location.hash = '#/system'
    const { result } = renderHook(() => usePageRoute())
    expect(result.current[0]).toBe('system')
    act(() => { result.current[1]('providers'); window.dispatchEvent(new HashChangeEvent('hashchange')) })
    expect(window.location.hash).toBe('#/providers'); expect(result.current[0]).toBe('providers')
    act(() => { window.location.hash = '#/targets'; window.dispatchEvent(new HashChangeEvent('hashchange')) })
    expect(result.current[0]).toBe('targets')
	act(() => { result.current[1]('mapping'); window.dispatchEvent(new HashChangeEvent('hashchange')) })
	expect(result.current[0]).toBe('mapping')
	act(() => { window.location.hash = '/xiaomi'; window.dispatchEvent(new HashChangeEvent('hashchange')) })
	expect(window.location.hash).toBe('#/providers'); expect(result.current[0]).toBe('providers')
  })
})
