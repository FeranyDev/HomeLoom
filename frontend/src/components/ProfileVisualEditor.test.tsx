import { render, screen } from '@testing-library/react'
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
})
