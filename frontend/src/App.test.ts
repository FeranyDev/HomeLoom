import { describe, expect, it } from 'vitest'
import { supportsProviderChildDevices } from './providerRouting'

describe('Provider child-device routing', () => {
	it('routes Camera Providers to their child-device manager', () => {
		expect(supportsProviderChildDevices('camera')).toBe(true)
		expect(supportsProviderChildDevices('mqtt')).toBe(true)
		expect(supportsProviderChildDevices('virtual')).toBe(false)
	})
})
