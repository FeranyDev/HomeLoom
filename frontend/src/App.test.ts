import { describe, expect, it } from 'vitest'
import { supportsProviderChildDevices } from './providerRouting'

describe('Provider child-device routing', () => {
	it('routes Providers with explicit child-device managers', () => {
		expect(supportsProviderChildDevices('camera')).toBe(true)
		expect(supportsProviderChildDevices('mqtt')).toBe(true)
		expect(supportsProviderChildDevices('virtual')).toBe(true)
		expect(supportsProviderChildDevices('xiaomi')).toBe(true)
		expect(supportsProviderChildDevices('gree')).toBe(true)
		expect(supportsProviderChildDevices('unknown')).toBe(false)
	})
})
