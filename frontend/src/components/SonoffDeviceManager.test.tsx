import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { SonoffDeviceManager } from './SonoffDeviceManager'

const api = vi.hoisted(() => ({ discoverSonoffDevices: vi.fn(), scanProviderNetwork: vi.fn() }))
vi.mock('../api/sonoff', () => ({ discoverSonoffDevices: api.discoverSonoffDevices }))
vi.mock('../api/providers', () => ({ scanProviderNetwork: api.scanProviderNetwork }))

const provider: Provider = {
	id: 'sonoff-main', type: 'sonoff', name: '易微联', enabled: true, status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: true, propertyWrite: true, commands: true, events: true },
	config: { mode: 'auto', cloud: { accessToken: '********' }, devices: [] },
}

describe('SonoffDeviceManager', () => {
	beforeEach(() => {
		vi.clearAllMocks()
		api.discoverSonoffDevices.mockResolvedValue([{ id: 'sonoff-1001f95735', deviceId: '1001f95735', name: '双路开关', model: 'DUALR3', uiid: 7, homeName: '我的家', roomName: '客厅', channels: 2, online: true, configured: false }])
		api.scanProviderNetwork.mockResolvedValue([])
	})

	it('adds a cloud device to the durable managed list before publishing it', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<SonoffDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '读取 eWeLink 设备目录' }))
		await waitFor(() => expect(api.discoverSonoffDevices).toHaveBeenCalledWith('sonoff-main'))
		expect(await screen.findByText('双路开关')).toBeInTheDocument()
		expect(screen.getByText('eWeLink 云可见')).toHaveClass('is-ready')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByText('已管理 1 台设备')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			id: 'sonoff-main', type: 'sonoff', config: expect.objectContaining({ managedDevices: true, devices: [expect.objectContaining({ id: 'sonoff-1001f95735', deviceId: '1001f95735', name: '双路开关', uiid: 7, channels: 2, type: 'switch' })] }),
		}), true))
	})

	it('lets the user select the unified device type before adding a device', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<SonoffDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '读取 eWeLink 设备目录' }))
		const selector = await screen.findByRole('combobox', { name: '双路开关 统一模型' })
		expect(selector).toHaveValue('switch')
		await userEvent.selectOptions(selector, 'outlet')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ devices: [expect.objectContaining({ deviceId: '1001f95735', type: 'outlet' })] }) }), true))
	})

	it('lets the user replace an eWeLink fallback name before saving the device', async () => {
		api.discoverSonoffDevices.mockResolvedValueOnce([{ id: 'sonoff-1001f95735', deviceId: '1001f95735', name: 'eWeLink_1001f95735', model: 'DUALR3', uiid: 7, channels: 2, online: true, configured: false }])
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<SonoffDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '读取 eWeLink 设备目录' }))
		const name = await screen.findByLabelText('设备 1001f95735 名称')
		expect(name).toHaveValue('eWeLink_1001f95735')
		await userEvent.clear(name)
		await userEvent.type(name, '门口微动开关')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ devices: [expect.objectContaining({ deviceId: '1001f95735', name: '门口微动开关' })] }) }), true))
	})

	it('updates the unified type of an already managed device', async () => {
		const saved = { ...provider, config: { ...provider.config, managedDevices: true, devices: [{ id: 'sonoff-1001f95735', deviceId: '1001f95735', name: '已保存开关', uiid: 7, channels: 2, type: 'switch' }] } }
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<SonoffDeviceManager provider={saved} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByRole('combobox', { name: '已保存开关 统一模型' }), 'lightbulb')
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ devices: [expect.objectContaining({ deviceId: '1001f95735', type: 'lightbulb' })] }) }), true))
	})

	it('keeps saved devices visible when the current cloud and LAN directories are empty', () => {
		const saved = { ...provider, config: { ...provider.config, managedDevices: true, devices: [{ id: 'sonoff-1001f95735', deviceId: '1001f95735', name: '已保存开关', uiid: 7, channels: 2 }] } }
		render(<SonoffDeviceManager provider={saved} devices={[]} onClose={() => {}} onSave={vi.fn()} />)
		expect(screen.getByText('已保存开关')).toBeInTheDocument()
		expect(screen.getByText('云端暂未返回')).toBeInTheDocument()
		expect(screen.getByText('已保存，等待刷新')).toBeInTheDocument()
		expect(screen.getByText('已管理 1 台设备')).toBeInTheDocument()
	})

	it('merges a LAN scan into a savable device entry', async () => {
		api.scanProviderNetwork.mockResolvedValue([{ id: 'sonoff-lan-plug', providerType: 'sonoff', name: '局域网插座', host: '192.168.1.30', port: 8081, mac: '', metadata: { deviceId: 'lan-plug', type: 'plug', encrypted: 'false', diy: 'true' } }])
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<SonoffDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '扫描 Sonoff 局域网' }))
		expect(await screen.findByText('局域网插座')).toBeInTheDocument()
		expect(screen.getByText(/局域网 192.168.1.30/)).toHaveClass('is-ready')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ devices: [expect.objectContaining({ deviceId: 'lan-plug', host: '192.168.1.30', port: 8081, diy: true })] }) }), true))
	})
})
