import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { XiaomiDeviceManager } from './XiaomiDeviceManager'

const api = vi.hoisted(() => ({ discoverXiaomiDevices: vi.fn() }))
vi.mock('../api/xiaomi', () => ({ discoverXiaomiDevices: api.discoverXiaomiDevices }))

const provider: Provider = {
	id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', enabled: true, status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: true, propertyWrite: true, commands: true, events: true },
	metrics: { cloudMqttConnected: 1 },
	config: { host: '192.168.1.50', port: 8883, clientId: '123', clientCertificate: '********', privateKey: '********', devices: [] },
}

describe('XiaomiDeviceManager', () => {
	beforeEach(() => { vi.clearAllMocks(); api.discoverXiaomiDevices.mockResolvedValue([{ did: '123.456', name: '客厅灯', model: 'vendor.light.v1', homeId: 'home-main', homeName: '我的家', roomId: 'room-living', roomName: '客厅', localIp: '192.168.1.20', localAvailable: true, gatewayAvailable: true, localControlAvailable: true, cloudAvailable: true, pushAvailable: true, specType: 'urn:miot-spec-v2:device:light:0000A001' }]) })

	it('discovers subdevices through the running provider and saves mappings', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<XiaomiDeviceManager provider={provider} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '从中枢读取子设备' }))
		await waitFor(() => expect(api.discoverXiaomiDevices).toHaveBeenCalledWith('xiaomi-main', 'xiaomi'))
		expect(await screen.findByText('客厅灯')).toBeInTheDocument()
		expect(screen.getByText(/我的家 \/ 客厅/)).toBeInTheDocument()
		expect(screen.getByText('中枢本地可控')).toHaveClass('is-ready')
		expect(screen.getByText('OAuth 官方云可用')).toHaveClass('is-ready')
		expect(screen.getByText('中枢实时')).toHaveClass('is-ready')
		expect(screen.getByText('官方云实时')).toHaveClass('is-ready')
		expect(screen.getByLabelText('客厅灯 统一模型')).toHaveValue('lightbulb')
		expect(screen.getByLabelText('客厅灯 连接策略')).toHaveValue('auto')
		await userEvent.click(screen.getByRole('button', { name: '加入映射' }))
		expect(screen.getByText('已映射 1 台设备')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ id: 'xiaomi-main', config: expect.objectContaining({ devices: [expect.objectContaining({ did: '123.456', type: 'lightbulb', connectionMode: 'auto', homeId: 'home-main', home: '我的家', roomId: 'room-living', room: '客厅' })] }) }), true))
	})

	it('allows changing a mapped central device route without reconnecting the provider', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const mapped = { ...provider, config: { ...provider.config, devices: [{ did: '123.456', id: 'xiaomi-123.456', name: '客厅灯', type: 'lightbulb', connectionMode: 'auto', properties: [], actions: [] }] } }
		render(<XiaomiDeviceManager provider={mapped} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '从中枢读取子设备' }))
		const route = await screen.findByLabelText('客厅灯 连接策略')
		await userEvent.selectOptions(route, 'cloud')
		expect(route).toHaveValue('cloud')
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ devices: [expect.objectContaining({ did: '123.456', connectionMode: 'cloud' })] }) }), true))
	})

	it('opens mapping configuration for a configured device missing from the device center', async () => {
		const onMapping = vi.fn()
		const mapped = { ...provider, config: { ...provider.config, devices: [{ did: '123.456', id: 'xiaomi-123.456', name: '客厅灯', type: 'lightbulb', connectionMode: 'auto', properties: [], actions: [] }] } }
		render(<XiaomiDeviceManager provider={mapped} onClose={() => {}} onSave={vi.fn()} onMapping={onMapping} />)
		await userEvent.click(screen.getByRole('button', { name: '配置 客厅灯 属性映射' }))
		expect(onMapping).toHaveBeenCalledWith(expect.objectContaining({ id: 'xiaomi-123.456', providerId: 'xiaomi-main', name: '客厅灯', type: 'lightbulb', endpoints: [] }))
	})

	it('requires an established MQTT connection', () => {
		render(<XiaomiDeviceManager provider={{ ...provider, status: 'failed' }} onClose={() => {}} onSave={vi.fn()} />)
		expect(screen.getByText(/状态变为 running 后才能读取设备目录/)).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '从中枢读取子设备' })).toBeDisabled()
		expect(screen.getByRole('button', { name: '保存子设备映射' })).toBeDisabled()
	})

	it('uses an isolated MIoT cloud directory and cloud-prefixed device ids', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const cloud: Provider = { ...provider, id: 'xiaomi-miot-cloud-main', type: 'xiaomi-miot-cloud', name: '小米 MIoT 云', capabilities: { ...provider.capabilities, events: false }, config: { region: 'cn', username: 'owner@example.com', password: '********', devices: [] } }
		render(<XiaomiDeviceManager provider={cloud} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '从 MIoT 云读取设备' }))
		await waitFor(() => expect(api.discoverXiaomiDevices).toHaveBeenCalledWith('xiaomi-miot-cloud-main', 'xiaomi-miot-cloud'))
		expect(screen.getByText(/局域网 MIoT 可用/)).toBeInTheDocument()
		expect(screen.getByLabelText('客厅灯 连接策略')).toHaveValue('auto')
		await userEvent.click(await screen.findByRole('button', { name: '加入映射' }))
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'xiaomi-miot-cloud', config: expect.objectContaining({ devices: [expect.objectContaining({ id: 'xiaomi-miot-123.456', connectionMode: 'auto' })] }) }), true))
	})
})
