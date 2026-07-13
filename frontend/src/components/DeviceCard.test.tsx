import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { DeviceCard } from './DeviceCard'
import type { Device } from '../types/device'

const sensorDevice = (type: Device['type'], capabilityId: string, propertyId: string, value: { type: 'bool'; bool: boolean } | { type: 'number'; number: number }): Device => ({
  id: type, providerId: 'virtual', name: type, type, online: true, state: {}, lastUpdateAt: new Date().toISOString(),
  endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [{ id: capabilityId, type, properties: [{ definition: { id: propertyId, name: propertyId, type: value.type, readable: true, writable: false, notifiable: true }, value }] }] }],
})

describe('DeviceCard device types', () => {
  it.each([['lightbulb', '灯泡'], ['outlet', '插座']] as const)('controls %s devices', async (type, label) => {
    const device: Device = { id: type, providerId: 'virtual', name: label, type, online: true, state: { power: false }, endpoints: [], lastUpdateAt: new Date().toISOString() }; const onPowerChange = vi.fn()
    render(<DeviceCard device={device} pending={false} onPowerChange={onPowerChange} onDetails={() => {}} />)
    expect(screen.getByRole('heading', { name: label })).toBeInTheDocument(); await userEvent.click(screen.getByRole('button', { name: /已关闭/ })); expect(onPowerChange).toHaveBeenCalledWith(device, true)
  })

  it.each([
    [sensorDevice('humidity-sensor', 'humidity', 'current-humidity', { type: 'number', number: 61.2 }), '61.2', '%'],
    [sensorDevice('contact-sensor', 'contact', 'contact-detected', { type: 'bool', bool: true }), '已闭合', 'CONTACT'],
    [sensorDevice('motion-sensor', 'motion', 'motion-detected', { type: 'bool', bool: true }), '检测到活动', 'MOTION'],
  ])('renders sensor state for %s', (device, value, unit) => {
    render(<DeviceCard device={device} pending={false} onPowerChange={() => {}} onDetails={() => {}} />)
    expect(screen.getByText(value)).toBeInTheDocument()
    expect(screen.getByText(unit)).toBeInTheDocument()
  })
})
