import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useMappingSection, usePageRoute } from './routing'

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

  it('supports mapping section deep links', () => {
    window.location.hash = '#/mapping/profiles'
    const page = renderHook(() => usePageRoute())
    const section = renderHook(() => useMappingSection())
    expect(page.result.current[0]).toBe('mapping')
    expect(section.result.current[0]).toBe('profiles')
    act(() => { section.result.current[1]('models'); window.dispatchEvent(new HashChangeEvent('hashchange')) })
    expect(window.location.hash).toBe('#/mapping/models')
    expect(section.result.current[0]).toBe('models')
  })
})
