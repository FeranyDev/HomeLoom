import { describe, expect, it } from 'vitest'
import type { Device } from './device'
import { deviceProperty } from './device'

describe('deviceProperty', () => {
  it('treats null endpoint collections from property-less camera payloads as empty', () => {
    const camera = {
      schemaVersion: 1,
      id: 'camera-1',
      providerId: 'xiaomi-main',
      name: 'Camera',
      type: 'camera',
      availability: 'online',
      online: true,
      endpoints: null,
      lastUpdateAt: new Date(0).toISOString(),
    } as unknown as Device

    expect(deviceProperty(camera, 'switch', 'power')).toBeUndefined()
  })
})
