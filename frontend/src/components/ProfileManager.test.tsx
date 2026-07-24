import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ProfileManager } from './ProfileManager'
import { storeProfileDraft } from '../profileDraft'

const builtIn = { schemaVersion: 1 as const, id: 'builtin-active-low', version: 1, kind: 'provider' as const, inputType: 'bool' as const, outputType: 'bool' as const, transforms: [{ type: 'invert' as const }], builtIn: true }
const custom = { schemaVersion: 1 as const, id: 'custom-map', version: 1, kind: 'capability' as const, inputType: 'number' as const, outputType: 'number' as const, transforms: [{ type: 'scale' as const, factor: 2 }], builtIn: false }

describe('ProfileManager', () => {
  it('lists protected built-ins and increments versions when editing', async () => {
    const api = { list: vi.fn().mockResolvedValue([builtIn, custom]), create: vi.fn(), update: vi.fn().mockResolvedValue({ ...custom, version: 2 }), remove: vi.fn(), importMany: vi.fn() }
    render(<ProfileManager api={api} />)
    expect(await screen.findByText('builtin-active-low')).toBeInTheDocument()
    expect(screen.getByText('内置只读')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '编辑' }))
    expect(screen.getByLabelText('Profile 版本')).toHaveValue(2)
    expect(screen.getByText('配置结构有效')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '保存并热更新' }))
    expect(api.update).toHaveBeenCalledWith('custom-map', expect.objectContaining({ id: 'custom-map', version: 2 }))
  })

  it('places editable profiles first and opens the editor before the saved list', async () => {
    const targetCustom = { ...custom, id: 'target-custom', kind: 'target' as const }
    const providerCustom = { ...custom, id: 'provider-custom', kind: 'provider' as const }
    const api = { list: vi.fn().mockResolvedValue([builtIn, targetCustom, providerCustom, custom]), create: vi.fn(), update: vi.fn(), remove: vi.fn(), importMany: vi.fn() }
    const { container } = render(<ProfileManager api={api} />)
    await screen.findByText('builtin-active-low')

    expect([...container.querySelectorAll<HTMLElement>('.profile-list article > strong')].map((item) => item.textContent)).toEqual([
      'provider-custom',
      'custom-map',
      'target-custom',
      'builtin-active-low',
    ])

    await userEvent.click(screen.getByRole('button', { name: '＋ 新建转换配置' }))
    const manager = container.querySelector('.profile-manager')!
    const editor = screen.getByLabelText('新建 Profile')
    const listHeading = screen.getByText('已保存配置').closest('.profile-list-heading')!
    expect([...manager.children].indexOf(editor)).toBeLessThan([...manager.children].indexOf(listHeading))
  })

  it('keeps raw JSON available as an advanced editing mode', async () => {
    const api = { list: vi.fn().mockResolvedValue([custom]), create: vi.fn(), update: vi.fn().mockResolvedValue({ ...custom, version: 2 }), remove: vi.fn(), importMany: vi.fn() }
    render(<ProfileManager api={api} />)
    await screen.findByText('custom-map')
    await userEvent.click(screen.getByRole('button', { name: '编辑' }))
    await userEvent.click(screen.getByRole('tab', { name: '高级 JSON' }))
    expect((screen.getByLabelText('Profile JSON') as HTMLTextAreaElement).value).toContain('"version": 2')
  })

  it('imports profile documents as one batch', async () => {
    const api = { list: vi.fn().mockResolvedValue([]), create: vi.fn(), update: vi.fn(), remove: vi.fn(), importMany: vi.fn().mockResolvedValue([]) }
    render(<ProfileManager api={api} />)
    await screen.findByText(/Profile.*管理/)
    await userEvent.click(screen.getByRole('button', { name: '导入 JSON' }))
    await userEvent.click(screen.getByRole('button', { name: '验证并导入' }))
    expect(api.importMany).toHaveBeenCalledWith([expect.objectContaining({ id: 'custom-profile', version: 1 })])
  })

  it('opens a prefilled draft when arriving from a mapping mismatch jump', async () => {
    const api = { list: vi.fn().mockResolvedValue([]), create: vi.fn(), update: vi.fn(), remove: vi.fn(), importMany: vi.fn() }
    storeProfileDraft({
      stage: 'provider',
      inputType: 'enum',
      outputType: 'enum',
      sourceEnum: ['Automatic', 'Silent'],
      targetEnum: ['auto', 'low'],
      sourceLabel: 'fan-level',
      targetLabel: 'fan-speed',
    })
    render(<ProfileManager api={api} />)
    expect(await screen.findByLabelText('第 1 步来源值 1')).toHaveValue('Automatic')
    expect(screen.getByLabelText('第 1 步目标值 1')).toHaveValue('auto')
    expect(screen.getByLabelText('第 1 步来源值 2')).toHaveValue('Silent')
    expect(screen.getByLabelText('第 1 步目标值 2')).toHaveValue('low')
  })
})
