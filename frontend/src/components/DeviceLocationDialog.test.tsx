import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Device } from '../types/device'
import { DeviceLocationDialog } from './DeviceLocationDialog'

const device: Device = {
	schemaVersion: 1, id: 'light', providerId: 'xiaomi-main', name: '客厅灯', type: 'lightbulb',
	availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-08-13T00:00:00Z',
	locationMode: 'source', homeId: 'source-home', homeName: '来源家庭', roomId: 'source-room', roomName: '来源客厅',
	sourceHomeId: 'source-home', sourceHomeName: '来源家庭', sourceRoomId: 'source-room', sourceRoomName: '来源客厅',
}

describe('DeviceLocationDialog', () => {
	it('shows the read-only source location and saves a custom HomeLoom group', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const homes = [{ id: 'home-main', name: '我的家', rooms: [{ id: 'room-bedroom', homeId: 'home-main', name: '卧室' }] }]
		render(<DeviceLocationDialog device={device} homes={homes} onManage={() => {}} onCancel={() => {}} onSave={onSave} />)
		expect(screen.getByText('来源家庭 / 来源客厅')).toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '关闭' })).not.toBeInTheDocument()
		expect(screen.getByRole('button', { name: '取消' })).toBeInTheDocument()
		await userEvent.selectOptions(screen.getByLabelText('位置策略'), 'custom')
		await userEvent.selectOptions(screen.getByLabelText('HomeLoom 家庭'), 'home-main')
		await userEvent.selectOptions(screen.getByLabelText('HomeLoom 房间'), 'room-bedroom')
		await userEvent.click(screen.getByRole('button', { name: '保存位置' }))
		expect(onSave).toHaveBeenCalledWith({ mode: 'custom', homeId: 'home-main', roomId: 'room-bedroom' })
	})

	it('restores source inheritance without sending custom names', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const custom = { ...device, locationMode: 'custom' as const, homeId: 'home-main', homeName: '我的家', roomId: 'room-study', roomName: '书房' }
		render(<DeviceLocationDialog device={custom} homes={[{ id: 'home-main', name: '我的家', rooms: [{ id: 'room-study', homeId: 'home-main', name: '书房' }] }]} onManage={() => {}} onCancel={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByLabelText('位置策略'), 'source')
		await userEvent.click(screen.getByRole('button', { name: '保存位置' }))
		expect(onSave).toHaveBeenCalledWith({ mode: 'source' })
	})
})
