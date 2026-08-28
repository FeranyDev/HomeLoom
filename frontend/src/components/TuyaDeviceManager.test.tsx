import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { TuyaDeviceManager } from './TuyaDeviceManager'

const api = vi.hoisted(() => ({ discoverTuyaDevices: vi.fn() }))
vi.mock('../api/tuya', () => ({ discoverTuyaDevices: api.discoverTuyaDevices }))

const provider: Provider = {
	id: 'tuya-main', type: 'tuya', name: '涂鸦云', enabled: true, status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: true, propertyWrite: true, commands: true, events: true },
	config: { authType: 'sharing', accessToken: '********', refreshToken: '********', devices: [] },
}

describe('TuyaDeviceManager', () => {
	beforeEach(() => {
		vi.clearAllMocks()
		api.discoverTuyaDevices.mockResolvedValue([{ id: 'tuya-device-1', deviceId: 'device-1', name: '微动开关', type: 'switch', category: 'kg', productName: 'Tuya Switch', homeName: '我的家', roomName: '门厅', online: true, configured: false, specification: { functions: [{ code: 'switch_1', type: 'Boolean' }] }, status: [{ code: 'switch_1', value: false }] }])
	})

	it('selects a cloud device, device type, and durable catalog before publishing', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TuyaDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '读取 Tuya 设备目录' }))
		expect(await screen.findByText('微动开关')).toBeInTheDocument()
		await userEvent.selectOptions(screen.getByRole('combobox', { name: '微动开关 统一模型' }), 'contact-sensor')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		const name = screen.getByLabelText('草稿设备 device-1 名称')
		await userEvent.clear(name)
		await userEvent.type(name, '门口微动开关')
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			id: 'tuya-main', type: 'tuya', config: expect.objectContaining({ managedDevices: true, devices: [expect.objectContaining({ id: 'tuya-device-1', deviceId: 'device-1', name: '门口微动开关', type: 'contact-sensor', specification: expect.any(Object), status: expect.any(Array) })] }),
		}), true))
	})

	it('keeps a saved device visible when the current cloud directory is empty', () => {
		const saved = { ...provider, config: { ...provider.config, managedDevices: true, devices: [{ id: 'tuya-device-1', deviceId: 'device-1', name: '已保存开关', type: 'switch', specification: {}, status: [] }] } }
		render(<TuyaDeviceManager provider={saved} devices={[]} onClose={() => {}} onSave={vi.fn()} />)
		expect(screen.getAllByText('已保存开关').length).toBeGreaterThan(0)
		expect(screen.getByText('已保存，等待刷新')).toBeInTheDocument()
		expect(screen.getByText('已管理 1 台设备')).toBeInTheDocument()
	})
})
