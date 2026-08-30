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

vi.mock('./BindingManager', () => ({
	BindingManager: ({ device }: { device: { name: string } }) => <div data-testid="consumer-mapping-editor">{device.name} 属性映射</div>,
}))

const target: Target = { id: 'apple-main', type: 'apple-hap', name: '主桥', enabled: true, status: 'running', config: { address: ':51826', setupId: 'HLM1' }, pairing: { pairingCode: '001-02-003', paired: false }, deviceIds: [], devices: [] }
const source: Device = { schemaVersion: 1, id: 'source-switch', providerId: 'virtual-main', name: '来源开关', type: 'switch', availability: 'online', online: true, lastUpdateAt: '2026-07-15T00:00:00Z', endpoints: [] }
const robot: Device = { ...source, id: 'source-vacuum', name: '扫地机器人', type: 'robot-vacuum' }
const climate: Device = { ...source, id: 'source-climate', name: '温湿度传感器', type: 'temperature-humidity-sensor' }

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

	it('expands each Consumer property mapping directly below its device entry', async () => {
		const configuredTarget: Target = {
			...target,
			devices: [
				{ id: 'living-switch', name: '客厅开关', type: 'switch', sourceDeviceId: source.id, enabled: true },
				{ id: 'bedroom-switch', name: '卧室开关', type: 'switch', sourceDeviceId: source.id, enabled: true },
			],
		}
		const { container } = render(<TargetDeviceManager target={configuredTarget} devices={[source]} onClose={() => {}} onSave={vi.fn()} />)

		await userEvent.click(screen.getAllByRole('button', { name: '配置属性映射' })[0])
		expect(await screen.findByText('客厅开关 · 属性映射')).toBeInTheDocument()

		const list = container.querySelector('.target-virtual-device-list')!
		const [livingDevice, bedroomDevice] = list.querySelectorAll('article')
		expect(livingDevice.querySelector('.target-source-mappings')).toBeInTheDocument()
		expect([...list.children].some((child) => child.classList.contains('target-source-mappings'))).toBe(false)
		expect(list.lastElementChild).toBe(bedroomDevice)
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

	it('requires an exact phrase and uses the explicit endpoint API for a Matter Device Type change', async () => {
		vi.mocked(listConsumerCatalogs).mockResolvedValue([{ id: 'matter', name: 'Matter', properties: [
			{ id: 'OnOff.OnOff', name: 'OnOff', deviceType: 'switch', defaultModelPath: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, level: 'required', type: 'bool', readable: true, writable: true, notifiable: true },
			{ id: 'OnOff.Light', name: 'Light', deviceType: 'lightbulb', defaultModelPath: { endpointId: 'main', capabilityId: 'lightbulb', propertyId: 'power' }, level: 'required', type: 'bool', readable: true, writable: true, notifiable: true },
		] }])
		const matter: Target = {
			id: 'matter-main', type: 'matter', name: 'Matter', enabled: true, status: 'running',
			config: {}, commissioning: { state: 'commissioned', windowOpen: false }, fabricCount: 1, endpointCount: 1,
			deviceIds: ['source-switch'], devices: [{ id: 'lamp', name: 'Lamp', type: 'switch', sourceDeviceId: 'source-switch', enabled: true }],
		}
		const onSave = vi.fn().mockResolvedValue(undefined)
		const onConfirm = vi.fn().mockResolvedValue(undefined)
		const prompt = vi.spyOn(window, 'prompt').mockReturnValue('CHANGE ENDPOINT TYPE matter-main lamp lightbulb')
		render(<TargetDeviceManager target={matter} devices={[source]} onClose={() => {}} onSave={onSave} onConfirmMatterEndpointType={onConfirm} />)
		await waitFor(() => expect(screen.getByLabelText('lamp 设备类型')).toBeEnabled())
		await userEvent.selectOptions(screen.getByLabelText('lamp 设备类型'), 'lightbulb')
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		await waitFor(() => expect(onConfirm).toHaveBeenCalledWith('lamp', 'lightbulb', 'CHANGE ENDPOINT TYPE matter-main lamp lightbulb'))
		expect(onSave.mock.calls[0][0].devices[0].type).toBe('switch')
		expect(prompt).toHaveBeenCalledWith(expect.stringContaining('CHANGE ENDPOINT TYPE matter-main lamp lightbulb'))
		prompt.mockRestore()
	})

	it('only offers protocol-standard Matter consumer device types', async () => {
		vi.mocked(listConsumerCatalogs).mockResolvedValue([{ id: 'matter', name: 'Matter', properties: [
			{ id: 'TemperatureMeasurement.MeasuredValue', name: '当前温度', deviceType: 'temperature-sensor', defaultModelPath: { endpointId: 'main', capabilityId: 'temperature', propertyId: 'current-temperature' }, level: 'required', type: 'number', readable: true, writable: false, notifiable: true },
			{ id: 'RelativeHumidityMeasurement.MeasuredValue', name: '当前湿度', deviceType: 'humidity-sensor', defaultModelPath: { endpointId: 'main', capabilityId: 'humidity', propertyId: 'current-humidity' }, level: 'required', type: 'number', readable: true, writable: false, notifiable: true },
		] }])
		const matter: Target = {
			id: 'matter-main', type: 'matter', name: 'Matter', enabled: true, status: 'running',
			config: {}, commissioning: { state: 'commissioned', windowOpen: false }, fabricCount: 1, endpointCount: 0,
			deviceIds: [], devices: [],
		}
		render(<TargetDeviceManager target={matter} devices={[climate]} onClose={() => {}} onSave={vi.fn()} />)
		const typeSelect = screen.getByLabelText('消费端设备类型') as HTMLSelectElement
		await waitFor(() => expect(Array.from(typeSelect.options, (option) => option.value)).toEqual([
			'', 'temperature-sensor', 'humidity-sensor',
		]))
	})

	it('includes the Matter Device Type in automatically generated consumer IDs', async () => {
		vi.mocked(listConsumerCatalogs).mockResolvedValue([{ id: 'matter', name: 'Matter', properties: [
			{ id: 'TemperatureMeasurement.MeasuredValue', name: '当前温度', deviceType: 'temperature-sensor', defaultModelPath: { endpointId: 'main', capabilityId: 'temperature', propertyId: 'current-temperature' }, level: 'required', type: 'number', readable: true, writable: false, notifiable: true },
			{ id: 'RelativeHumidityMeasurement.MeasuredValue', name: '当前湿度', deviceType: 'humidity-sensor', defaultModelPath: { endpointId: 'main', capabilityId: 'humidity', propertyId: 'current-humidity' }, level: 'required', type: 'number', readable: true, writable: false, notifiable: true },
		] }])
		const matter: Target = {
			id: 'matter-jmsjfa', type: 'matter', name: 'Matter', enabled: true, status: 'running',
			config: {}, commissioning: { state: 'commissioned', windowOpen: false }, fabricCount: 2, endpointCount: 0,
			deviceIds: [], devices: [],
		}
		const xiaomiClimate = { ...climate, id: 'xiaomi-blt-3-1p04292pl0400' }
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetDeviceManager target={matter} devices={[xiaomiClimate]} onClose={() => {}} onSave={onSave} />)
		const typeSelect = screen.getByLabelText('消费端设备类型')
		await waitFor(() => expect(typeSelect).toHaveValue('temperature-sensor'))
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.selectOptions(typeSelect, 'humidity-sensor')
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(onSave.mock.calls[0][0].devices.map((item: { id: string }) => item.id)).toEqual([
			'matter-jmsjfa-xiaomi-blt-3-1p04292pl0400-temperature-sensor',
			'matter-jmsjfa-xiaomi-blt-3-1p04292pl0400-humidity-sensor',
		])
	})

	it('preserves the Matter Device Type suffix when generated IDs exceed 64 characters', async () => {
		vi.mocked(listConsumerCatalogs).mockResolvedValue([{ id: 'matter', name: 'Matter', properties: [
			{ id: 'TemperatureMeasurement.MeasuredValue', name: '当前温度', deviceType: 'temperature-sensor', defaultModelPath: { endpointId: 'main', capabilityId: 'temperature', propertyId: 'current-temperature' }, level: 'required', type: 'number', readable: true, writable: false, notifiable: true },
		] }])
		const matter: Target = {
			id: `matter-${'bridge'.repeat(8)}`, type: 'matter', name: 'Matter', enabled: true, status: 'running',
			config: {}, commissioning: { state: 'commissioned', windowOpen: false }, fabricCount: 0, endpointCount: 0,
			deviceIds: [], devices: [],
		}
		const longSource = { ...climate, id: `source-${'climate'.repeat(8)}` }
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetDeviceManager target={matter} devices={[longSource]} onClose={() => {}} onSave={onSave} />)
		await waitFor(() => expect(screen.getByLabelText('消费端设备类型')).toHaveValue('temperature-sensor'))
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		const generatedIDs = onSave.mock.calls[0][0].devices.map((item: { id: string }) => item.id)
		expect(new Set(generatedIDs).size).toBe(2)
		for (const generatedID of generatedIDs) {
			expect(generatedID).toHaveLength(64)
			expect(generatedID).toMatch(/-temperature-sensor$/)
		}
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
		await userEvent.selectOptions(screen.getByLabelText('来源设备家庭'), 'name:父母家')
		await waitFor(() => expect(screen.getByLabelText('来源统一设备')).toHaveValue('source-vacuum'))
		expect(screen.queryByRole('option', { name: /来源开关/ })).not.toBeInTheDocument()
		expect(screen.getByRole('option', { name: /扫地机器人.*父母家 \/ 卧室/ })).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await userEvent.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ devices: [expect.objectContaining({ sourceDeviceId: 'source-vacuum' })] }))
	})
})
