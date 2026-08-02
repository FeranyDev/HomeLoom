import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { GreeDeviceManager } from './GreeDeviceManager'
import type { Provider } from '../types/provider'

const providerAPI = vi.hoisted(() => ({ scanProviderNetwork: vi.fn() }))
vi.mock('../api/providers', () => providerAPI)

const provider: Provider = {
	id: 'gree-main', type: 'gree', name: '格力空调来源', enabled: true, status: 'running', retryCount: 0,
	config: { devices: [], pollIntervalSeconds: 60, requestTimeoutSeconds: 5 },
	capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: true },
}

describe('GreeDeviceManager', () => {
	beforeEach(() => vi.clearAllMocks())

	it('scans the LAN, deduplicates candidates, and saves selected devices', async () => {
		providerAPI.scanProviderNetwork.mockResolvedValue([
			{ id: 'gree-aabbccddeeff', providerType: 'gree', name: '客厅空调', host: '192.168.1.42', port: 7000, mac: 'AA:BB:CC:DD:EE:FF' },
			{ id: 'gree-aabbccddeeff', providerType: 'gree', name: '重复响应', host: '192.168.1.42', port: 7000, mac: 'aa-bb-cc-dd-ee-ff' },
			{ id: 'gree-112233445566', providerType: 'gree', name: '卧室空调', host: '192.168.1.43', port: 7000, mac: '112233445566' },
		])
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<GreeDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '扫描局域网设备' }))
		await waitFor(() => expect(providerAPI.scanProviderNetwork).toHaveBeenCalledWith(expect.objectContaining({ type: 'gree', config: expect.objectContaining({ devices: [] }) })))
		expect(screen.getAllByRole('button', { name: '加入设备' })).toHaveLength(2)
		await userEvent.click(screen.getAllByRole('button', { name: '加入设备' })[0])
		expect(screen.getByRole('button', { name: '已加入' })).toBeDisabled()
		await userEvent.click(screen.getByRole('button', { name: '加入设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'gree', config: expect.objectContaining({ devices: [
			expect.objectContaining({ host: '192.168.1.42', mac: 'aabbccddeeff' }),
			expect.objectContaining({ host: '192.168.1.43', mac: '112233445566' }),
		] }) }), true))
	})

	it('adds and edits a device with the visual form', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<GreeDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '手动添加' }))
		await userEvent.clear(screen.getByLabelText('格力设备 ID'))
		await userEvent.type(screen.getByLabelText('格力设备 ID'), 'living-room-gree')
		await userEvent.type(screen.getByLabelText('格力设备名称'), '客厅空调')
		await userEvent.type(screen.getByLabelText('格力设备地址'), '192.168.1.42')
		await userEvent.type(screen.getByLabelText('格力设备 MAC'), 'AA:BB:CC:DD:EE:FF')
		await userEvent.type(screen.getByLabelText('格力加密密钥'), 'secret')
		await userEvent.selectOptions(screen.getByLabelText('格力加密版本'), '2')
		await userEvent.click(screen.getByRole('button', { name: '加入设备' }))
		expect(screen.getByText('已添加格力设备“客厅空调”。')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ devices: [expect.objectContaining({ id: 'living-room-gree', encryptionVersion: 2, encryptionKey: 'secret' })] }) }), true))
	})

	it('does not allow scanning while the Provider is offline', () => {
		render(<GreeDeviceManager provider={{ ...provider, status: 'error' }} devices={[]} onClose={() => {}} onSave={vi.fn()} />)
		expect(screen.getByRole('alert')).toHaveTextContent('状态变为 running')
		expect(screen.getByRole('button', { name: '扫描局域网设备' })).toBeDisabled()
	})
})
