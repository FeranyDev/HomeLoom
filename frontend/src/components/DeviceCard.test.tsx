import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { DeviceCard } from './DeviceCard'
import type { Device } from '../types/device'

const sensorDevice = (type: Device['type'], capabilityId: string, propertyId: string, value: { type: 'bool'; bool: boolean } | { type: 'number'; number: number }, unit?: string): Device => ({
  schemaVersion: 1, id: type, providerId: 'virtual', name: type, type, availability: 'online', online: true, lastUpdateAt: new Date().toISOString(),
  endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [{ id: capabilityId, type, properties: [{ definition: { id: propertyId, name: propertyId, type: value.type, unit, readable: true, writable: false, notifiable: true }, value }] }] }],
})

describe('DeviceCard device types', () => {
	it.each([
		['local', '局域网'],
		['cloud', '云端轮询'],
		['pending', '等待判定'],
	] as const)('renders Xiaomi cloud runtime mode %s', (runtimeMode, label) => {
		const device = { ...sensorDevice('temperature-sensor', 'temperature', 'current-temperature', { type: 'number', number: 20 }), providerId: 'xiaomi-miot-cloud-main', runtimeMode }
		render(<DeviceCard device={device} pending={false} onPowerChange={() => {}} onDetails={() => {}} />)
		expect(screen.getByText(label)).toHaveClass('device-runtime-mode', `is-${runtimeMode}`)
	})

	it.each([
		['local-mqtt', '中枢实时'],
		['cloud-mqtt', '官方云实时'],
		['cloud-http', '官方云校准'],
	] as const)('renders the precise Xiaomi state transport %s', (stateTransport, label) => {
		const device = { ...sensorDevice('temperature-sensor', 'temperature', 'current-temperature', { type: 'number', number: 20 }), providerId: 'xiaomi-main', runtimeMode: stateTransport === 'local-mqtt' ? 'local' as const : 'cloud' as const, stateTransport }
		render(<DeviceCard device={device} pending={false} onPowerChange={() => {}} onDetails={() => {}} />)
		expect(screen.getByText(label)).toBeInTheDocument()
	})

	it('opens mapping configuration from the corresponding device card', async () => {
		const device = sensorDevice('temperature-sensor', 'temperature', 'current-temperature', { type: 'number', number: 20 }, 'celsius')
		const onMapping = vi.fn()
		render(<DeviceCard device={device} pending={false} onPowerChange={() => {}} onDetails={() => {}} onMapping={onMapping} />)
		expect(screen.getByRole('article', { name: 'temperature-sensor' })).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '配置映射' }))
		expect(onMapping).toHaveBeenCalledWith(device)
	})

	it('exposes persistent disable separately from provider availability', async () => {
		const device = sensorDevice('temperature-sensor', 'temperature', 'current-temperature', { type: 'number', number: 20 }, 'celsius')
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
    [sensorDevice('humidity-sensor', 'humidity', 'current-humidity', { type: 'number', number: 61.2 }, 'percent'), '61.2', '%'],
    [sensorDevice('pressure-sensor', 'pressure', 'current-pressure', { type: 'number', number: 1013.2 }, 'hectopascal'), '1013.2', 'hPa'],
    [sensorDevice('contact-sensor', 'contact', 'contact-detected', { type: 'bool', bool: true }), '已闭合', 'CONTACT'],
    [sensorDevice('motion-sensor', 'motion', 'motion-detected', { type: 'bool', bool: true }), '检测到活动', 'MOTION'],
  ])('renders sensor state for %s', (device, value, unit) => {
    render(<DeviceCard device={device} pending={false} onPowerChange={() => {}} onDetails={() => {}} />)
    expect(screen.getByText(value)).toBeInTheDocument()
    expect(screen.getByText(unit)).toBeInTheDocument()
  })

	it('renders both measurements for a temperature/humidity sensor', () => {
		const device: Device = {
			schemaVersion: 1, id: 'climate', providerId: 'virtual', name: '温湿度', type: 'temperature-humidity-sensor', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(),
			endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [
				{ id: 'temperature', type: 'temperature-sensor', properties: [{ definition: { id: 'current-temperature', name: '当前温度', type: 'number', readable: true, writable: false, notifiable: true }, value: { type: 'number', number: 22.4 } }] },
				{ id: 'humidity', type: 'humidity-sensor', properties: [{ definition: { id: 'current-humidity', name: '当前湿度', type: 'number', readable: true, writable: false, notifiable: true }, value: { type: 'number', number: 48.5 } }] },
			] }],
		}
		render(<DeviceCard device={device} pending={false} onPowerChange={() => {}} onDetails={() => {}} />)
		expect(screen.getByText(/温湿度传感器.*temperature-humidity-sensor/)).toBeInTheDocument()
		expect(screen.getByText('22.4')).toBeInTheDocument()
		expect(screen.getByText('48.5')).toBeInTheDocument()
	})

	it('renders advanced HomeKit device summaries', () => {
		const purifier: Device = { schemaVersion: 1, id: 'air', providerId: 'virtual', name: '净化器', type: 'air-purifier', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(), endpoints: [{ id: 'main', name: 'Main', type: 'air-purifier', capabilities: [{ id: 'air-purifier', type: 'air-purifier', properties: [{ definition: { id: 'active', name: '启用', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: true } }, { definition: { id: 'rotation-speed', name: '速度', type: 'number', readable: true, writable: true, notifiable: true }, value: { type: 'number', number: 60 } }] }, { id: 'filter', type: 'filter-maintenance', properties: [{ definition: { id: 'life-level', name: '寿命', type: 'number', readable: true, writable: false, notifiable: true }, value: { type: 'number', number: 82 } }] }] }] }
		render(<DeviceCard device={purifier} pending={false} onPowerChange={() => {}} onDetails={() => {}} />)
		expect(screen.getByText(/空气净化器.*air-purifier/)).toBeInTheDocument(); expect(screen.getByText('运行中 · 60%')).toBeInTheDocument(); expect(screen.getByText('AIR · 滤芯 82%')).toBeInTheDocument()
	})
})
