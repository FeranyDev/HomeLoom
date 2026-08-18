import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { XiaomiDeviceManager } from './XiaomiDeviceManager'

const api = vi.hoisted(() => ({ discoverXiaomiDevices: vi.fn(), setDeviceLocation: vi.fn() }))
vi.mock('../api/xiaomi', () => ({ discoverXiaomiDevices: api.discoverXiaomiDevices }))
vi.mock('../api/devices', () => ({ setDeviceLocation: api.setDeviceLocation }))

const provider: Provider = {
	id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', enabled: true, status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: true, propertyWrite: true, commands: true, events: true },
	metrics: { cloudMqttConnected: 1 },
	config: { host: '192.168.1.50', port: 8883, clientId: '123', clientCertificate: '********', privateKey: '********', devices: [] },
}

describe('XiaomiDeviceManager', () => {
	beforeEach(() => { vi.clearAllMocks(); api.setDeviceLocation.mockResolvedValue({}); api.discoverXiaomiDevices.mockResolvedValue([{ did: '123.456', name: '客厅灯', model: 'vendor.light.v1', homeId: 'home-main', homeName: '我的家', roomId: 'room-living', roomName: '客厅', localIp: '192.168.1.20', localAvailable: true, gatewayAvailable: true, localControlAvailable: true, cloudAvailable: true, pushAvailable: true, specType: 'urn:miot-spec-v2:device:light:0000A001' }]) })

	it('discovers subdevices through the running provider and saves mappings', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<XiaomiDeviceManager provider={provider} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '从中枢读取子设备' }))
		await waitFor(() => expect(api.discoverXiaomiDevices).toHaveBeenCalledWith('xiaomi-main', 'xiaomi'))
		expect(await screen.findByText('客厅灯')).toBeInTheDocument()
		expect(screen.getAllByText(/我的家 \/ 客厅/).length).toBeGreaterThan(0)
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

	it('generates unsaved drafts by DID without merging same-name mappings', async () => {
		api.discoverXiaomiDevices.mockResolvedValue([
			{ did: 'already-mapped', name: '客厅灯', model: 'vendor.light.v1' },
			{ did: 'same-name-new-device', name: '客厅灯', model: 'vendor.light.v2' },
		])
		const onSave = vi.fn().mockResolvedValue(undefined)
		const mapped = { ...provider, config: { ...provider.config, devices: [{ did: 'already-mapped', id: 'xiaomi-already-mapped', name: '客厅灯', type: 'lightbulb', connectionMode: 'auto', properties: [], actions: [] }] } }
		render(<XiaomiDeviceManager provider={mapped} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '从中枢读取子设备' }))
		await screen.findAllByText('客厅灯')
		await userEvent.click(screen.getByRole('button', { name: '为未映射设备生成草稿' }))
		expect(screen.getByText('已映射 2 台设备')).toBeInTheDocument()
		expect(onSave).not.toHaveBeenCalled()
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ devices: expect.arrayContaining([
			expect.objectContaining({ did: 'already-mapped', id: 'xiaomi-already-mapped' }),
			expect.objectContaining({ did: 'same-name-new-device', id: 'xiaomi-same-name-new-device' }),
		]) }) }), true))
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

	it('inherits source location by default and can save a HomeLoom custom location while adding a device', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const locations = [{ id: 'home-parents', name: '父母家', rooms: [{ id: 'room-bedroom', homeId: 'home-parents', name: '卧室' }] }]
		render(<XiaomiDeviceManager provider={provider} locations={locations} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '从中枢读取子设备' }))
		const locationMode = await screen.findByLabelText('客厅灯 位置策略')
		expect(locationMode).toHaveValue('source')
		expect(screen.getByRole('option', { name: /继承来源：我的家 \/ 客厅/ })).toBeInTheDocument()
		await userEvent.selectOptions(locationMode, 'custom')
		await userEvent.selectOptions(screen.getByLabelText('客厅灯 HomeLoom 家庭'), 'home-parents')
		await userEvent.selectOptions(screen.getByLabelText('客厅灯 HomeLoom 房间'), 'room-bedroom')
		await userEvent.click(screen.getByRole('button', { name: '加入映射' }))
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))
		await waitFor(() => expect(api.setDeviceLocation).toHaveBeenCalledWith('xiaomi-123.456', { mode: 'custom', homeId: 'home-parents', roomId: 'room-bedroom' }))
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

	it('adds a discovered camera with a Xiaomi MISS media profile', async () => {
		api.discoverXiaomiDevices.mockResolvedValue([{
			did: 'camera.did.1',
			name: '客厅摄像头',
			model: 'isa.camera.hlc7',
			localIp: '192.168.1.30',
			localAvailable: true,
			specType: 'urn:miot-spec-v2:device:camera:0000A01C',
		}])
		const onSave = vi.fn().mockResolvedValue(undefined)
		const cloud: Provider = {
			...provider,
			id: 'xiaomi-miot-cloud-main',
			type: 'xiaomi-miot-cloud',
			name: '小米 MIoT 云',
			config: { region: 'cn', userId: '42', ssecurity: '********', serviceToken: '********', passToken: '********', devices: [] },
		}
		render(<XiaomiDeviceManager provider={cloud} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '从 MIoT 云读取设备' }))
		expect(await screen.findByLabelText('客厅摄像头 统一模型')).toHaveValue('camera')
		await userEvent.click(screen.getByRole('button', { name: '加入映射' }))
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({
				passToken: '********',
				devices: [expect.objectContaining({
					did: 'camera.did.1',
					type: 'camera',
					model: 'isa.camera.hlc7',
					properties: [],
					media: {
						protocol: 'xiaomi-miss',
						subtype: 'hd',
						channel: 1,
						profiles: [expect.objectContaining({
							schemaVersion: 1,
							id: 'main',
							videoCodec: 'h264',
							audioCodec: 'aac',
						})],
					},
				})],
			}),
		}), true))
	})

	it('maps a central-hub camera as a control-only camera source', async () => {
		api.discoverXiaomiDevices.mockResolvedValue([{
			did: 'camera.did.1',
			name: '客厅摄像头',
			model: 'isa.camera.hlc7',
			gatewayAvailable: true,
			localControlAvailable: true,
			cloudAvailable: true,
			pushAvailable: true,
			specType: 'urn:miot-spec-v2:device:camera:0000A01C',
		}])
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<XiaomiDeviceManager provider={provider} onClose={() => {}} onSave={onSave} />)

		await userEvent.click(screen.getByRole('button', { name: '从中枢读取子设备' }))
		const model = await screen.findByLabelText('客厅摄像头 统一模型')
		expect(model).toHaveValue('camera')
		expect(screen.getByRole('option', { name: '摄像头（camera）' })).toBeInTheDocument()
		expect(screen.getByText(/只提供中枢\/云端控制能力/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '加入映射' }))
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))

		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			type: 'xiaomi',
			config: expect.objectContaining({
				devices: [expect.objectContaining({
					did: 'camera.did.1',
					id: 'xiaomi-control-camera.did.1',
					type: 'camera',
					properties: [],
					actions: [],
				})],
			}),
		}), true))
		const saved = (onSave.mock.calls[0][0].config.devices as Array<Record<string, unknown>>)[0]
		expect(saved).not.toHaveProperty('media')
	})

	it('repairs a persisted central-camera id that collides with the Camera Provider', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const conflicting = {
			...provider,
			config: {
				...provider.config,
				devices: [{
					did: '1178028045',
					id: 'xiaomi-1178028045',
					name: '小米智能摄像机',
					type: 'camera',
					connectionMode: 'auto',
					properties: [],
					actions: [],
				}],
			},
		}
		render(<XiaomiDeviceManager provider={conflicting} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '保存子设备映射' }))

		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({
				devices: [expect.objectContaining({
					did: '1178028045',
					id: 'xiaomi-control-1178028045',
					type: 'camera',
				})],
			}),
		}), true))
	})

	it('filters the discovery directory by home and room before mapping', async () => {
		api.discoverXiaomiDevices.mockResolvedValue([
			{ did: '123.456', name: '客厅灯', homeId: 'home-main', homeName: '我的家', roomId: 'room-living', roomName: '客厅' },
			{ did: '789.000', name: '卧室空调', homeId: 'home-parents', homeName: '父母家', roomId: 'room-bedroom', roomName: '卧室' },
		])
		render(<XiaomiDeviceManager provider={provider} onClose={() => {}} onSave={vi.fn()} />)
		await userEvent.click(screen.getByRole('button', { name: '从中枢读取子设备' }))
		await screen.findByText('客厅灯')
		await userEvent.selectOptions(screen.getByLabelText('小米设备家庭'), 'name:父母家')
		expect(screen.getByText('卧室空调')).toBeInTheDocument()
		expect(screen.queryByText('客厅灯')).not.toBeInTheDocument()
		await userEvent.selectOptions(screen.getByLabelText('小米设备房间'), 'name:父母家::name:卧室')
		expect(screen.getByText('1 / 2 台')).toBeInTheDocument()
	})
})
