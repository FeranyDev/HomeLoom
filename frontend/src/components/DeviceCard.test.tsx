import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { DeviceCard } from './DeviceCard'
import type { Device } from '../types/device'

const sensorDevice = (type: Device['type'], capabilityId: string, propertyId: string, value: { type: 'bool'; bool: boolean } | { type: 'number'; number: number }): Device => ({
  schemaVersion: 1, id: type, providerId: 'virtual', name: type, type, availability: 'online', online: true, lastUpdateAt: new Date().toISOString(),
  endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [{ id: capabilityId, type, properties: [{ definition: { id: propertyId, name: propertyId, type: value.type, readable: true, writable: false, notifiable: true }, value }] }] }],
})

describe('DeviceCard device types', () => {
	it('exposes persistent disable separately from provider availability', async () => {
		const device = sensorDevice('temperature-sensor', 'temperature', 'current-temperature', { type: 'number', number: 20 })
		const onEnabledChange = vi.fn()
		const { rerender } = render(<DeviceCard device={device} pending={false} onPowerChange={() => {}} onDetails={() => {}} onEnabledChange={onEnabledChange} />)
		await userEvent.click(screen.getByRole('button', { name: '禁用设备' })); expect(onEnabledChange).toHaveBeenCalledWith(device, false)
		const disabled = { ...device, availability: 'offline' as const, online: false, disabled: true }
		rerender(<DeviceCard device={disabled} pending={false} onPowerChange={() => {}} onDetails={() => {}} onEnabledChange={onEnabledChange} />)
		expect(screen.getByText('已禁用')).toBeInTheDocument(); await userEvent.click(screen.getByRole('button', { name: '重新启用' })); expect(onEnabledChange).toHaveBeenLastCalledWith(disabled, true)
	})

  it.each([['lightbulb', '灯泡'], ['outlet', '插座']] as const)('controls %s devices', async (type, label) => {
    const device: Device = { schemaVersion: 1, id: type, providerId: 'virtual', name: label, type, availability: 'online', online: true, endpoints: [{ id: 'main', name: 'Main', type, capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: '开关', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } }] }] }], lastUpdateAt: new Date().toISOString() }; const onPowerChange = vi.fn()
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

	it('renders advanced HomeKit device summaries', () => {
		const purifier: Device = { schemaVersion: 1, id: 'air', providerId: 'virtual', name: '净化器', type: 'air-purifier', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [{ id: 'main', name: 'Main', type: 'air-purifier', capabilities: [{ id: 'air-purifier', type: 'air-purifier', properties: [{ definition: { id: 'active', name: '启用', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: true } }, { definition: { id: 'rotation-speed', name: '速度', type: 'number', readable: true, writable: true, notifiable: true }, value: { type: 'number', number: 60 } }] }, { id: 'filter', type: 'filter-maintenance', properties: [{ definition: { id: 'life-level', name: '寿命', type: 'number', readable: true, writable: false, notifiable: true }, value: { type: 'number', number: 82 } }] }] }] }
		render(<DeviceCard device={purifier} pending={false} onPowerChange={() => {}} onDetails={() => {}} />)
		expect(screen.getByText('空气净化器')).toBeInTheDocument(); expect(screen.getByText('运行中 · 60%')).toBeInTheDocument(); expect(screen.getByText('AIR · 滤芯 82%')).toBeInTheDocument()
	})
})
