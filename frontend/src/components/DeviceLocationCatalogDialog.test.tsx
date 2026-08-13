import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DeviceLocationCatalogDialog } from './DeviceLocationCatalogDialog'

const api = vi.hoisted(() => ({
	createHome: vi.fn(), createRoom: vi.fn(), deleteHome: vi.fn(), deleteRoom: vi.fn(), list: vi.fn(), updateHome: vi.fn(), updateRoom: vi.fn(),
}))
vi.mock('../api/devices', () => ({
	createDeviceLocationHome: api.createHome,
	createDeviceLocationRoom: api.createRoom,
	deleteDeviceLocationHome: api.deleteHome,
	deleteDeviceLocationRoom: api.deleteRoom,
	listDeviceLocations: api.list,
	updateDeviceLocationHome: api.updateHome,
	updateDeviceLocationRoom: api.updateRoom,
}))

describe('DeviceLocationCatalogDialog', () => {
	beforeEach(() => { vi.clearAllMocks(); api.createHome.mockResolvedValue({}); api.createRoom.mockResolvedValue({}); api.list.mockResolvedValue([]) })

	it('creates configured homes and rooms and refreshes the directory', async () => {
		const onChange = vi.fn()
		const homes = [{ id: 'home-main', name: '我的家', rooms: [] }]
		api.list.mockResolvedValue(homes)
		const view = render(<DeviceLocationCatalogDialog homes={[]} onChange={onChange} onClose={() => {}} />)
		expect(screen.getByRole('button', { name: '关闭' })).toHaveClass('location-catalog-close')
		await userEvent.type(screen.getByLabelText('新家庭名称'), '我的家')
		await userEvent.click(screen.getByRole('button', { name: '添加家庭' }))
		await waitFor(() => expect(api.createHome).toHaveBeenCalledWith('我的家'))
		expect(onChange).toHaveBeenCalledWith(homes)

		view.rerender(<DeviceLocationCatalogDialog homes={homes} onChange={onChange} onClose={() => {}} />)
		await userEvent.type(screen.getByLabelText('我的家 新房间名称'), '客厅')
		await userEvent.click(screen.getByRole('button', { name: '添加房间' }))
		await waitFor(() => expect(api.createRoom).toHaveBeenCalledWith('home-main', '客厅'))
	})
})
