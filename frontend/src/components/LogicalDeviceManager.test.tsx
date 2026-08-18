import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { LogicalDeviceManager } from './LogicalDeviceManager'

const devices = [
  { schemaVersion: 1, id: 'local-light', providerId: 'local', name: '客厅主灯', type: 'switch', availability: 'online' as const, online: true, endpoints: [], lastUpdateAt: '2026-08-01T00:00:00Z' },
  { schemaVersion: 1, id: 'cloud-light', providerId: 'cloud', name: '客厅主灯', type: 'switch', availability: 'online' as const, online: true, endpoints: [], lastUpdateAt: '2026-08-01T00:00:00Z' },
]

describe('LogicalDeviceManager', () => {
  it('requires a manual save after applying a location-backed candidate', async () => {
    const api = { list: vi.fn().mockResolvedValue([]), candidates: vi.fn().mockResolvedValue([{ left: { providerId: 'local', deviceId: 'local-light', name: '客厅主灯', type: 'switch', homeId: 'home-main' }, right: { providerId: 'cloud', deviceId: 'cloud-light', name: '客厅主灯', type: 'switch', homeId: 'home-main' }, reasons: ['same_type', 'same_normalized_name', 'same_source_home'] }]), save: vi.fn().mockResolvedValue({}), remove: vi.fn(), explanations: vi.fn() }
    render(<LogicalDeviceManager devices={devices} onClose={vi.fn()} onChanged={vi.fn().mockResolvedValue(undefined)} api={api} />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /使用候选/ }))
    expect(screen.getByLabelText('逻辑设备名称')).toHaveValue('客厅主灯')
    await user.type(screen.getByLabelText('逻辑设备 ID'), 'living-light')
    await user.click(screen.getByRole('button', { name: '保存链接' }))
    await waitFor(() => expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ bindings: [{ providerId: 'local', deviceId: 'local-light', priority: 0 }, { providerId: 'cloud', deviceId: 'cloud-light', priority: 10 }] }), false))
  })
})
