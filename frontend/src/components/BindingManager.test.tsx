import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Device } from '../types/device'
import { BindingManager } from './BindingManager'

const device: Device = {
  schemaVersion: 1, id: 'virtual-switch-1', providerId: 'virtual-main', name: 'Virtual Switch', type: 'switch', availability: 'online', online: true, lastUpdateAt: new Date().toISOString(),
  endpoints: [{ id: 'main', name: 'Main', type: 'main', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: 'Power', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } }] }] }],
}

describe('BindingManager', () => {
  it('creates an exact property binding from compatible profiles', async () => {
    const create = vi.fn(async (input) => ({ ...input, id: 'binding-one' }))
    const api = {
      listBindings: vi.fn(async () => []),
		listProfiles: vi.fn(async () => [{ schemaVersion: 1 as const, id: 'builtin-active-low', version: 1, kind: 'provider' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: true }]),
      create, update: vi.fn(), remove: vi.fn(),
    }
    render(<BindingManager devices={[device]} api={api} />)
    await waitFor(() => expect(screen.getByLabelText('绑定设备')).toHaveValue('virtual-switch-1'))
    await waitFor(() => expect(screen.getByLabelText('绑定 Profile')).toHaveValue('builtin-active-low'))
    await userEvent.click(screen.getByRole('button', { name: '＋ 创建并实时应用' }))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.objectContaining({ providerId: 'virtual-main', deviceId: 'virtual-switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', profileId: 'builtin-active-low', enabled: true })))
  })

  it('filters out incompatible and non-reversible profiles', async () => {
    const api = {
      listBindings: vi.fn(async () => []),
      listProfiles: vi.fn(async () => [
		{ schemaVersion: 1 as const, id: 'target-map', version: 1, kind: 'target' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: false },
		{ schemaVersion: 1 as const, id: 'number-map', version: 1, kind: 'provider' as const, inputType: 'number' as const, outputType: 'number' as const, transforms: [{ type: 'scale' as const, factor: 2 }], builtIn: false },
      ]),
      create: vi.fn(), update: vi.fn(), remove: vi.fn(),
    }
    render(<BindingManager devices={[device]} api={api} />)
    await screen.findByText('设备属性绑定')
    await waitFor(() => expect(screen.getByLabelText('绑定 Profile')).toHaveValue(''))
    expect(screen.queryByRole('option', { name: /target-map/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /number-map/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '＋ 创建并实时应用' })).toBeDisabled()
  })
})
