import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { discoverXiaomiDevices } from '../api/xiaomi'
import type { Provider } from '../types/provider'
import { CameraDeviceManager } from './CameraDeviceManager'

vi.mock('../api/xiaomi', () => ({ discoverXiaomiDevices: vi.fn() }))

const provider: Provider = {
	id: 'camera-main', type: 'camera', name: '家庭摄像头', enabled: true,
	config: { cameras: [] }, status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: false, propertyWrite: false, events: false },
}

describe('CameraDeviceManager', () => {
	it('adds an RTSP camera as a child device and saves through its Provider', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<CameraDeviceManager provider={provider} onClose={() => {}} onSave={onSave} />)
		expect(screen.getByText(/Camera Kernel 已启用/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '手动添加' }))
		await userEvent.type(screen.getByLabelText('摄像头 ID'), 'front-door')
		await userEvent.type(screen.getByLabelText('摄像头名称'), '门口摄像头')
		await userEvent.type(screen.getByLabelText('RTSP Host'), '192.168.1.20')
		await userEvent.type(screen.getByLabelText('RTSP Path'), '/live/main')
		expect(screen.getByText(/还没有可用的控制来源/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByText('门口摄像头')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			id: 'camera-main',
			config: expect.objectContaining({ cameras: [expect.objectContaining({ id: 'front-door', driver: 'rtsp', connectionMode: 'on_demand' })] }),
		}), true)
	})

	it('configures an always-on camera pipeline for fastest opening', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<CameraDeviceManager provider={provider} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '手动添加' }))
		await userEvent.type(screen.getByLabelText('摄像头 ID'), 'fast-camera')
		await userEvent.type(screen.getByLabelText('摄像头名称'), '快速摄像头')
		await userEvent.selectOptions(screen.getByLabelText('摄像头连接模式'), 'always_on')
		await userEvent.type(screen.getByLabelText('RTSP Host'), '192.168.1.22')
		await userEvent.type(screen.getByLabelText('RTSP Path'), '/live')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByText(/rtsp · 长连接 · 已启用/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({
				cameras: [expect.objectContaining({ id: 'fast-camera', connectionMode: 'always_on' })],
			}),
		}), true)
	})

	it('adds ONVIF as a distinct constrained input', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<CameraDeviceManager provider={provider} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '手动添加' }))
		await userEvent.type(screen.getByLabelText('摄像头 ID'), 'garage')
		await userEvent.type(screen.getByLabelText('摄像头名称'), '车库')
		await userEvent.selectOptions(screen.getByLabelText('摄像头驱动'), 'onvif')
		await userEvent.type(screen.getByLabelText('ONVIF Host'), '192.168.1.21')
		await userEvent.type(screen.getByLabelText('ONVIF 用户名'), 'viewer')
		await userEvent.type(screen.getByLabelText('ONVIF 密码'), 'secret')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({
				cameras: [expect.objectContaining({
					id: 'garage', driver: 'onvif',
					onvif: expect.objectContaining({ host: '192.168.1.21', port: 80 }),
				})],
			}),
		}), true)
	})

	it('configures Xiaomi MISS subtype from a visual selector', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<CameraDeviceManager provider={provider} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '手动添加' }))
		await userEvent.type(screen.getByLabelText('摄像头 ID'), 'xiaomi-front')
		await userEvent.type(screen.getByLabelText('摄像头名称'), '小米门口摄像头')
		await userEvent.selectOptions(screen.getByLabelText('摄像头驱动'), 'xiaomi-miss')
		expect(screen.getByLabelText('小米摄像头视频子类型')).toHaveValue('hd')
		await userEvent.selectOptions(screen.getByLabelText('小米摄像头视频子类型'), 'sd')
		await userEvent.type(screen.getByLabelText('小米摄像头 DID'), 'did-front')
		await userEvent.type(screen.getByLabelText('小米摄像头型号'), 'chuangmi.camera.079ac1')
		await userEvent.type(screen.getByLabelText('小米摄像头局域网 IP'), '192.168.1.30')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({ cameras: [expect.objectContaining({
				xiaomi: expect.objectContaining({ subtype: 'sd' }),
			})] }),
		}), true)
	})

	it('does not allow child-device mutation before the Camera Provider runs', () => {
		render(<CameraDeviceManager provider={{ ...provider, status: 'disabled', enabled: false }} onClose={() => {}} onSave={vi.fn()} />)
		expect(screen.getByRole('button', { name: '扫描摄像头' })).toBeDisabled()
		expect(screen.getByRole('button', { name: '手动添加' })).toBeDisabled()
		expect(screen.getByRole('button', { name: '保存设备并应用' })).toBeDisabled()
		expect(screen.getByRole('alert')).toHaveTextContent('请先启用 Camera Provider')
	})

	it('scans running Xiaomi directories and opens a discovered camera for confirmation', async () => {
		vi.mocked(discoverXiaomiDevices).mockResolvedValueOnce([
			{ did: '12345', name: '客厅摄像头', model: 'chuangmi.camera.079ac1', localIp: '192.168.1.30', homeName: '家', roomName: '客厅' },
			{ did: '67890', name: '台灯', model: 'yeelink.light.color1' },
		])
		const account: Provider = {
			...provider, id: 'xiaomi-cloud', type: 'xiaomi-miot-cloud', name: '米家账号',
			config: { region: 'cn' },
		}
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<CameraDeviceManager provider={provider} providers={[provider, account]} onClose={() => {}} onSave={onSave} />)

		await userEvent.click(screen.getByRole('button', { name: '扫描摄像头' }))
		expect(discoverXiaomiDevices).toHaveBeenCalledWith('xiaomi-cloud', 'xiaomi-miot-cloud')
		expect(await screen.findByText('客厅摄像头')).toBeInTheDocument()
		expect(screen.queryByText('台灯')).not.toBeInTheDocument()

		await userEvent.click(screen.getByRole('button', { name: '配置并加入' }))
		expect(screen.getByLabelText('摄像头 ID')).toHaveValue('xiaomi-12345')
		expect(screen.getByLabelText('摄像头名称')).toHaveValue('客厅摄像头')
		expect(screen.getByLabelText('小米摄像头 DID')).toHaveValue('12345')
		expect(screen.getByLabelText('小米摄像头型号')).toHaveValue('chuangmi.camera.079ac1')
		expect(screen.getByLabelText('小米摄像头局域网 IP')).toHaveValue('192.168.1.30')
		expect(screen.getByLabelText('小米摄像头账号认证')).toHaveValue('xiaomi-cloud')
		expect(screen.queryByLabelText('小米摄像头 Pass Token')).not.toBeInTheDocument()

		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({
				cameras: [expect.objectContaining({
					xiaomi: expect.objectContaining({ credentialProviderRef: 'xiaomi-cloud' }),
				})],
			}),
		}), true)
	})

	it('binds only a Xiaomi camera control source without copying credentials', async () => {
		const hub: Provider = {
			...provider,
			id: 'xiaomi-hub',
			type: 'xiaomi',
			name: '客厅中枢',
			config: {
				devices: [
					{ id: 'xiaomi-camera-control', did: 'camera-did', name: '客厅摄像头控制', type: 'camera' },
					{ id: 'xiaomi-light', did: 'light-did', name: '客厅灯', type: 'lightbulb' },
				],
			},
		}
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<CameraDeviceManager provider={provider} providers={[provider, hub]} devices={[
			{ schemaVersion: 1, id: 'xiaomi-camera-control', providerId: 'xiaomi-hub', name: '客厅摄像头控制', type: 'camera', availability: 'online', online: true, endpoints: [], lastUpdateAt: '' },
			{ schemaVersion: 1, id: 'xiaomi-light', providerId: 'xiaomi-hub', name: '客厅灯', type: 'lightbulb', availability: 'online', online: true, endpoints: [], lastUpdateAt: '' },
		]} onClose={() => {}} onSave={onSave} />)

		await userEvent.click(screen.getByRole('button', { name: '手动添加' }))
		await userEvent.type(screen.getByLabelText('摄像头 ID'), 'front-door')
		await userEvent.type(screen.getByLabelText('摄像头名称'), '门口摄像头')
		await userEvent.type(screen.getByLabelText('RTSP Host'), '192.168.1.20')
		await userEvent.type(screen.getByLabelText('RTSP Path'), '/live')
		expect(screen.getByRole('option', { name: /客厅摄像头控制/ })).toBeInTheDocument()
		expect(screen.queryByRole('option', { name: /客厅灯/ })).not.toBeInTheDocument()
		await userEvent.selectOptions(screen.getByLabelText('摄像头控制来源'), 'xiaomi-hub\u0000xiaomi-camera-control')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByText(/控制：客厅中枢 \/ 客厅摄像头控制/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))

		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({
				cameras: [expect.objectContaining({
					control: { providerRef: 'xiaomi-hub', deviceId: 'xiaomi-camera-control' },
				})],
			}),
		}), true)
		const savedControl = ((onSave.mock.calls[0][0].config.cameras as Array<Record<string, unknown>>)[0].control)
		expect(savedControl).toEqual({ providerRef: 'xiaomi-hub', deviceId: 'xiaomi-camera-control' })
	})

	it('repairs a legacy Xiaomi camera id before offering it as a control source', async () => {
		const hub: Provider = {
			...provider,
			id: 'xiaomi-hub',
			type: 'xiaomi',
			name: '客厅中枢',
			config: {
				devices: [{
					id: 'xiaomi-1178028045',
					did: '1178028045',
					name: '小米智能摄像机',
					type: 'camera',
				}],
			},
		}
		render(<CameraDeviceManager provider={provider} providers={[provider, hub]} onClose={() => {}} onSave={vi.fn()} />)
		await userEvent.click(screen.getByRole('button', { name: '手动添加' }))

		expect(screen.getByRole('option', { name: /小米智能摄像机/ })).toHaveValue('xiaomi-hub\u0000xiaomi-control-1178028045')
	})

	it('preserves an unavailable existing control binding and allows clearing it', async () => {
		const configured: Provider = {
			...provider,
			config: {
				cameras: [{
					id: 'front-door', name: '门口摄像头', driver: 'rtsp', connectionMode: 'on_demand', enabled: true,
					profiles: [], rtsp: { host: '192.168.1.20', port: 554, path: '/live' },
					control: { providerRef: 'xiaomi-offline', deviceId: 'camera-control' },
				}],
			},
		}
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<CameraDeviceManager provider={configured} providers={[configured]} onClose={() => {}} onSave={onSave} />)

		expect(screen.getByText(/xiaomi-offline \/ camera-control（暂不可用）/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '编辑' }))
		expect(screen.getByLabelText('摄像头控制来源')).toHaveValue('xiaomi-offline\u0000camera-control')
		expect(screen.getByText(/原控制设备当前未出现在目录中/)).toBeInTheDocument()
		await userEvent.selectOptions(screen.getByLabelText('摄像头控制来源'), '')
		await userEvent.click(screen.getByRole('button', { name: '更新草稿' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.objectContaining({ cameras: [expect.not.objectContaining({ control: expect.anything() })] }),
		}), true)
	})
})
