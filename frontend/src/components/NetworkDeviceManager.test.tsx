import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { NetworkDeviceManager } from './NetworkDeviceManager'

const provider: Provider = {
	id: 'network-main', type: 'network', name: '网络设备', enabled: true, status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: false, propertyWrite: true, events: true },
	config: { probeMethod: 'tcp', probeIntervalSeconds: 30, devices: [] },
}

describe('NetworkDeviceManager', () => {
	it('stages a manual network device and applies it through the shared save action', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<NetworkDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		expect(screen.getByRole('list', { name: '统一设备添加流程' })).toBeInTheDocument()
		expect(screen.getByText(/模型固定为 network-device/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '手动添加设备' }))
		await userEvent.clear(screen.getByLabelText('网络设备 ID'))
		await userEvent.type(screen.getByLabelText('网络设备 ID'), 'living-room-pc')
		await userEvent.type(screen.getByLabelText('网络设备名称'), '客厅电脑')
		await userEvent.type(screen.getByLabelText('网络设备 Host'), '192.168.1.20')
		await userEvent.clear(screen.getByLabelText('网络设备探测端口'))
		await userEvent.type(screen.getByLabelText('网络设备探测端口'), '445')
		await userEvent.type(screen.getByLabelText('网络设备 MAC'), 'AA:BB:CC:DD:EE:FF')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByText('客厅电脑')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			type: 'network', config: expect.objectContaining({ devices: [expect.objectContaining({ id: 'living-room-pc', name: '客厅电脑', host: '192.168.1.20', probeMethod: 'tcp', probePort: 445, mac: 'AA:BB:CC:DD:EE:FF' })] }),
		}), true)
	})

	it('requires a TCP port before a device can enter the draft catalog', async () => {
		render(<NetworkDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={vi.fn()} />)
		await userEvent.click(screen.getByRole('button', { name: '手动添加设备' }))
		await userEvent.type(screen.getByLabelText('网络设备名称'), '客厅电脑')
		await userEvent.type(screen.getByLabelText('网络设备 Host'), '192.168.1.20')
		await userEvent.clear(screen.getByLabelText('网络设备探测端口'))
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByRole('alert')).toHaveTextContent('TCP 探测端口')
	})
})
