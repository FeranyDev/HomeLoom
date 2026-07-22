import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Device } from '../types/device'
import type { Target } from '../types/target'
import { ApiError } from '../api/client'
import { listConsumerCatalogs } from '../api/mapping'
import { TargetDeviceManager } from './TargetDeviceManager'

vi.mock('../api/mapping', async (importOriginal) => {
	const actual = await importOriginal<typeof import('../api/mapping')>()
	return { ...actual, listConsumerCatalogs: vi.fn() }
})

const target: Target = { id: 'apple-main', type: 'apple-hap', name: '主桥', enabled: true, status: 'running', address: ':51826', setupId: 'HLM1', pairingCode: '001-02-003', deviceIds: [], devices: [] }
const source: Device = { schemaVersion: 1, id: 'source-switch', providerId: 'virtual-main', name: '来源开关', type: 'switch', availability: 'online', online: true, lastUpdateAt: '2026-07-15T00:00:00Z', endpoints: [] }
const robot: Device = { ...source, id: 'source-vacuum', name: '扫地机器人', type: 'robot-vacuum' }

describe('TargetDeviceManager', () => {
	beforeEach(() => {
		vi.mocked(listConsumerCatalogs).mockResolvedValue([{ id: 'homekit', name: 'Apple Home / HomeKit', properties: [{ id: 'Switch.On', name: 'Switch.On', deviceType: 'switch', defaultModelPath: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, level: 'required', type: 'bool', readable: true, writable: true, notifiable: true }] }])
	})

	it('creates a Target-owned Consumer device before property mapping', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetDeviceManager target={target} devices={[source]} onClose={() => {}} onSave={onSave} />)
		await userEvent.type(screen.getByLabelText(/显示名称/), '客厅 HomeKit 开关')
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		expect(screen.getByDisplayValue('客厅 HomeKit 开关')).toBeInTheDocument()
		expect(screen.getAllByText(/source-switch/).length).toBeGreaterThan(0)
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ devices: [expect.objectContaining({ id: 'apple-main-source-switch', sourceDeviceId: 'source-switch', type: 'switch' })] }))
	})

	it('allows one unified source to back multiple independently scoped Consumer devices', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetDeviceManager target={target} devices={[source]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		expect(screen.getByText('apple-main-source-switch-2')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(onSave.mock.calls[0][0].devices).toHaveLength(2)
	})

	it('shows the exact invalid Consumer device field returned by the API', async () => {
		const onSave = vi.fn().mockRejectedValue(new ApiError('invalid target configuration', 400, { 'devices.0.type': 'unified model "robot-vacuum" is not supported by consumer "homekit"' }))
		render(<TargetDeviceManager target={target} devices={[source]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(await screen.findByRole('alert')).toHaveTextContent('devices.0.type: unified model "robot-vacuum" is not supported by consumer "homekit"')
	})

	it('allows a source model to bind as a different HomeKit device type', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetDeviceManager target={target} devices={[robot, source]} onClose={() => {}} onSave={onSave} />)
		expect(await screen.findByRole('option', { name: /扫地机器人.*source-vacuum/ })).toBeEnabled()
		await waitFor(() => expect(screen.getByLabelText('消费端设备类型')).toHaveValue('switch'))
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ devices: [expect.objectContaining({ sourceDeviceId: 'source-vacuum', type: 'switch' })] }))
	})

	it('adds an auxiliary source to the same HomeKit device', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetDeviceManager target={target} devices={[source, robot]} onClose={() => {}} onSave={onSave} />)
		await userEvent.click(await screen.findByRole('checkbox', { name: /扫地机器人/ }))
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ devices: [expect.objectContaining({ sourceDeviceId: 'source-switch', auxiliarySourceDeviceIds: ['source-vacuum'] })] }))
	})

	it('filters selectable main and auxiliary sources by home and room', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const living = { ...source, homeId: 'home-a', homeName: '我的家', roomId: 'living', roomName: '客厅' }
		const parents = { ...robot, homeId: 'home-b', homeName: '父母家', roomId: 'bedroom', roomName: '卧室' }
		render(<TargetDeviceManager target={target} devices={[living, parents]} onClose={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByLabelText('来源设备家庭'), 'id:home-b')
		await waitFor(() => expect(screen.getByLabelText('来源统一设备')).toHaveValue('source-vacuum'))
		expect(screen.queryByRole('option', { name: /来源开关/ })).not.toBeInTheDocument()
		expect(screen.getByRole('option', { name: /扫地机器人.*父母家 \/ 卧室/ })).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ devices: [expect.objectContaining({ sourceDeviceId: 'source-vacuum' })] }))
	})
})
