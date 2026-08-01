import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { VirtualDeviceManager } from './VirtualDeviceManager'

const provider: Provider = {
	id: 'virtual-lab', type: 'virtual', name: '实验室虚拟设备', enabled: true, config: { latencyMs: 0, devices: [] },
	status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: true, propertyWrite: true, commands: true, events: true },
}

describe('VirtualDeviceManager', () => {
	it('creates a typed child device and saves it under the existing Provider', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<VirtualDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)

		await userEvent.click(screen.getByRole('button', { name: '添加虚拟设备' }))
		await userEvent.clear(screen.getByLabelText('虚拟设备 ID'))
		await userEvent.type(screen.getByLabelText('虚拟设备 ID'), 'living-room-light')
		await userEvent.clear(screen.getByLabelText('虚拟设备名称'))
		await userEvent.type(screen.getByLabelText('虚拟设备名称'), '客厅灯')
		await userEvent.selectOptions(screen.getByLabelText('虚拟设备模型'), 'lightbulb')
		await userEvent.clear(screen.getByLabelText('初始亮度'))
		await userEvent.type(screen.getByLabelText('初始亮度'), '72')
		await userEvent.click(screen.getByRole('button', { name: '加入虚拟设备' }))

		expect(screen.getByText('客厅灯')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存子设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			id: 'virtual-lab', type: 'virtual', config: expect.objectContaining({
				devices: [expect.objectContaining({ id: 'living-room-light', name: '客厅灯', type: 'lightbulb', brightness: 72, power: true })],
			}),
		}), true)
	})

	it('rejects duplicate child IDs before changing the staged catalog', async () => {
		const existing = { id: 'desk-switch', name: '桌面开关', type: 'switch', availability: 'online', online: true, power: false }
		render(<VirtualDeviceManager provider={{ ...provider, config: { devices: [existing] } }} devices={[]} onClose={() => {}} onSave={vi.fn()} />)

		await userEvent.click(screen.getByRole('button', { name: '添加虚拟设备' }))
		await userEvent.clear(screen.getByLabelText('虚拟设备 ID'))
		await userEvent.type(screen.getByLabelText('虚拟设备 ID'), 'desk-switch')
		await userEvent.click(screen.getByRole('button', { name: '加入虚拟设备' }))

		expect(screen.getByRole('alert')).toHaveTextContent('已经存在')
		expect(screen.getAllByText('桌面开关')).toHaveLength(1)
	})
})
