import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ProfileManager } from './ProfileManager'

const builtIn = { schemaVersion: 1 as const, id: 'builtin-active-low', version: 1, kind: 'provider' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: true }
const custom = { schemaVersion: 1 as const, id: 'custom-map', version: 1, kind: 'capability' as const, inputType: 'number' as const, outputType: 'number' as const, transforms: [{ type: 'scale' as const, factor: 2 }], builtIn: false }

describe('ProfileManager', () => {
  it('lists protected built-ins and increments versions when editing', async () => {
    const api = { list: vi.fn().mockResolvedValue([builtIn, custom]), create: vi.fn(), update: vi.fn().mockResolvedValue({ ...custom, version: 2 }), remove: vi.fn(), importMany: vi.fn() }
    render(<ProfileManager api={api} />)
    expect(await screen.findByText('builtin-active-low')).toBeInTheDocument()
    expect(screen.getByText('内置只读')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '编辑' }))
    expect((screen.getByLabelText('Profile JSON') as HTMLTextAreaElement).value).toContain('"version": 2')
    await userEvent.click(screen.getByRole('button', { name: '保存并热更新' }))
    expect(api.update).toHaveBeenCalledWith('custom-map', expect.objectContaining({ id: 'custom-map', version: 2 }))
  })

  it('imports profile documents as one batch', async () => {
    const api = { list: vi.fn().mockResolvedValue([]), create: vi.fn(), update: vi.fn(), remove: vi.fn(), importMany: vi.fn().mockResolvedValue([]) }
    render(<ProfileManager api={api} />)
    await screen.findByText(/Profile.*管理/)
    await userEvent.click(screen.getByRole('button', { name: '导入 JSON' }))
    await userEvent.click(screen.getByRole('button', { name: '验证并导入' }))
    expect(api.importMany).toHaveBeenCalledWith([expect.objectContaining({ id: 'custom-profile', version: 1 })])
  })
})
