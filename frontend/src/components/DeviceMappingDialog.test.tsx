import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Device } from '../types/device'
import { DeviceMappingDialog } from './DeviceMappingDialog'

vi.mock('./BindingManager', () => ({ BindingManager: () => <div>映射工作区</div> }))

const device: Device = {
	schemaVersion: 1,
	id: 'climate-1',
	providerId: 'xiaomi-main',
	name: '卧室温湿度',
	type: 'temperature-humidity-sensor',
	availability: 'online',
	online: true,
	lastUpdateAt: '2026-07-15T08:00:00Z',
	endpoints: [],
}

describe('DeviceMappingDialog', () => {
	it('renders as a centered modal and closes from both controls', async () => {
		const onClose = vi.fn()
		const { container } = render(<DeviceMappingDialog device={device} onClose={onClose} />)

		const dialog = screen.getByRole('dialog', { name: '卧室温湿度映射配置' })
		expect(dialog).toHaveAttribute('aria-modal', 'true')
		expect(container.querySelector('.modal-backdrop')).toHaveClass('is-mapping', 'is-centered')
		expect(screen.getByText('映射工作区')).toBeInTheDocument()

		await userEvent.click(screen.getByRole('button', { name: '关闭' }))
		await userEvent.keyboard('{Escape}')
		expect(onClose).toHaveBeenCalledTimes(2)
	})
})
