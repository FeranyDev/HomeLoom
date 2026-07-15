import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Device } from '../types/device'
import type { Target } from '../types/target'
import { TargetDeviceManager } from './TargetDeviceManager'

const target: Target = { id: 'apple-main', type: 'apple-hap', name: '主桥', enabled: true, status: 'running', address: ':51826', setupId: 'HLM1', pairingCode: '001-02-003', deviceIds: [], devices: [] }
const source: Device = { schemaVersion: 1, id: 'source-switch', providerId: 'virtual-main', name: '来源开关', type: 'switch', availability: 'online', online: true, lastUpdateAt: '2026-07-15T00:00:00Z', endpoints: [] }

describe('TargetDeviceManager', () => {
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
})
