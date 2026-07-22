import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { MappingProfile } from '../types/mapping'
import { ProfileVisualEditor } from './ProfileVisualEditor'

const identityProfile: MappingProfile = {
  schemaVersion: 1,
  id: 'temperature-normalizer',
  version: 1,
  kind: 'provider',
  inputType: 'int',
  outputType: 'int',
  transforms: [],
}

describe('ProfileVisualEditor', () => {
  it('visually builds a transform chain, derives its output type, previews and saves it', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    const runPreview = vi.fn().mockResolvedValue({
      profileId: 'temperature-normalizer', profileVersion: 1, direction: 'forward',
      value: { type: 'number', number: 2 },
      steps: [{ index: 0, transform: 'scale', input: { type: 'int', int: 20 }, output: { type: 'number', number: 2 } }],
    })
    render(<ProfileVisualEditor initialProfile={identityProfile} editing={false} saving={false} onClose={vi.fn()} onSave={onSave} runPreview={runPreview} />)

    expect(screen.getByText('恒等转换（identity）')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /数值缩放/ }))
    await userEvent.clear(screen.getByLabelText('第 1 步缩放系数'))
    await userEvent.type(screen.getByLabelText('第 1 步缩放系数'), '0.1')
    expect(screen.getAllByText('数值（number）').length).toBeGreaterThan(0)

    await userEvent.click(screen.getByRole('button', { name: '运行当前草稿' }))
    expect(runPreview).toHaveBeenCalledWith(expect.objectContaining({
      direction: 'forward', value: { type: 'int', int: 20 },
      profile: expect.objectContaining({ outputType: 'number', transforms: [{ type: 'scale', factor: 0.1, offset: 0 }] }),
    }))
    expect(await screen.findByText('20 → 2')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '保存并热更新' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ outputType: 'number', transforms: [{ type: 'scale', factor: 0.1, offset: 0 }] }))
  })

  it('only offers compatible steps and marks clamp pipelines as forward-only', async () => {
    render(<ProfileVisualEditor initialProfile={{ ...identityProfile, inputType: 'number', outputType: 'number' }} editing={false} saving={false} onClose={vi.fn()} onSave={vi.fn()} runPreview={vi.fn()} />)
    expect(screen.getByRole('button', { name: /布尔反转/ })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: /范围裁剪/ }))
    expect(screen.getByRole('button', { name: '反向（reverse）' })).toBeDisabled()
    expect(screen.getByText(/不会出现在当前设备属性映射/)).toBeInTheDocument()
  })

  it('switches input families without leaving incompatible transforms behind', async () => {
    render(<ProfileVisualEditor initialProfile={{ ...identityProfile, transforms: [{ type: 'scale', factor: 2 }], outputType: 'number' }} editing={false} saving={false} onClose={vi.fn()} onSave={vi.fn()} runPreview={vi.fn()} />)
    await userEvent.selectOptions(screen.getByLabelText('Profile 输入类型'), 'bool')
    expect(screen.queryByLabelText('第 1 步缩放系数')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /布尔反转/ })).toBeEnabled()
    expect(screen.getByRole('button', { name: /数值缩放/ })).toBeDisabled()
  })

  it('visually configures numeric ranges as a reversible enum', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    const runPreview = vi.fn().mockResolvedValue({
      profileId: identityProfile.id, profileVersion: 1, direction: 'forward',
      value: { type: 'enum', string: 'comfortable' },
      steps: [{ index: 0, transform: 'range-enum', input: { type: 'int', int: 20 }, output: { type: 'enum', string: 'comfortable' } }],
    })
    render(<ProfileVisualEditor initialProfile={identityProfile} editing={false} saving={false} onClose={vi.fn()} onSave={onSave} runPreview={runPreview} />)

    await userEvent.click(screen.getByRole('button', { name: /数值分段转枚举/ }))
    expect(screen.getByLabelText('第 1 步分段上限 1')).toHaveValue(18)
    expect(screen.getByLabelText('第 1 步分段枚举 2')).toHaveValue('comfortable')
    expect(screen.getByLabelText('第 1 步分段反向值 3')).toHaveValue(32)
    expect(screen.getByRole('button', { name: '反向（reverse）' })).toBeEnabled()

    await userEvent.click(screen.getByRole('button', { name: '运行当前草稿' }))
    expect(runPreview).toHaveBeenCalledWith(expect.objectContaining({ profile: expect.objectContaining({ outputType: 'enum', transforms: [expect.objectContaining({ type: 'range-enum', bands: expect.any(Array) })] }) }))
    expect(await screen.findByText('20 → comfortable')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '保存并热更新' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ inputType: 'int', outputType: 'enum', transforms: [expect.objectContaining({ type: 'range-enum' })] }))
  })

  it('groups paired conversions together and supports enum to number', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(<ProfileVisualEditor initialProfile={{ ...identityProfile, inputType: 'enum', outputType: 'enum' }} editing={false} saving={false} onClose={vi.fn()} onSave={onSave} runPreview={vi.fn()} />)

    const numberEnumGroup = screen.getByText('数值 ⇄ 枚举').closest('section')!
    expect(within(numberEnumGroup).getByRole('button', { name: /数值分段转枚举/ })).toBeDisabled()
    expect(within(numberEnumGroup).getByRole('button', { name: /枚举转数值/ })).toBeEnabled()

    const boolEnumGroup = screen.getByText('布尔 ⇄ 枚举').closest('section')!
    expect(within(boolEnumGroup).getByRole('button', { name: /布尔转枚举/ })).toBeDisabled()
    expect(within(boolEnumGroup).getByRole('button', { name: /枚举转布尔/ })).toBeEnabled()

    await userEvent.click(within(numberEnumGroup).getByRole('button', { name: /枚举转数值/ }))
    expect(screen.getAllByText('枚举转数值（enum-number）').length).toBeGreaterThan(1)
    expect(screen.getAllByText('数值（number）').length).toBeGreaterThan(0)
    await userEvent.click(screen.getByRole('button', { name: '保存并热更新' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ inputType: 'enum', outputType: 'number', transforms: [expect.objectContaining({ type: 'enum-number' })] }))
  })

  it('supports the missing bool to number direction', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(<ProfileVisualEditor initialProfile={{ ...identityProfile, inputType: 'bool', outputType: 'bool' }} editing={false} saving={false} onClose={vi.fn()} onSave={onSave} runPreview={vi.fn()} />)

    const numberBoolGroup = screen.getByText('数值 ⇄ 布尔').closest('section')!
    expect(within(numberBoolGroup).getByRole('button', { name: /数值阈值转布尔/ })).toBeDisabled()
    await userEvent.click(within(numberBoolGroup).getByRole('button', { name: /布尔转数值/ }))
    await userEvent.click(screen.getByRole('button', { name: '保存并热更新' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ inputType: 'bool', outputType: 'number', transforms: [expect.objectContaining({ type: 'bool-number' })] }))
  })
})
