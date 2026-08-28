import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Device } from '../types/device'
import { DeviceNameDialog } from './DeviceNameDialog'

const device: Device = {
	schemaVersion: 1, id: 'sonoff-1001f95735', providerId: 'sonoff-main', name: '门口微动开关', sourceName: 'eWeLink_1001f95735', nameOverridden: true,
	type: 'contact-sensor', availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-08-28T00:00:00Z',
}

describe('DeviceNameDialog', () => {
	it('saves a unified name and shows the provider source name', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<DeviceNameDialog device={{ ...device, nameOverridden: false, name: 'eWeLink_1001f95735' }} onCancel={() => {}} onReset={vi.fn()} onSave={onSave} />)
		expect(screen.getByText('eWeLink_1001f95735')).toBeInTheDocument()
		await userEvent.clear(screen.getByLabelText('设备显示名称'))
		await userEvent.type(screen.getByLabelText('设备显示名称'), '门口微动开关')
		await userEvent.click(screen.getByRole('button', { name: '保存名称' }))
		expect(onSave).toHaveBeenCalledWith('门口微动开关')
	})

	it('can restore the latest source name from an existing override', async () => {
		const onReset = vi.fn().mockResolvedValue(undefined)
		render(<DeviceNameDialog device={device} onCancel={() => {}} onReset={onReset} onSave={vi.fn()} />)
		expect(screen.getByText('当前正在使用 HomeLoom 自定义名称。恢复后会采用最新的设备源名称。')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '恢复来源名称' }))
		expect(onReset).toHaveBeenCalledOnce()
	})
})
