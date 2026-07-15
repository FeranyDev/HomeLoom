import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { MappingPreview } from './MappingPreview'

describe('MappingPreview', () => {
  it('submits a typed scale profile and explains the result', async () => {
    const runPreview = vi.fn().mockResolvedValue({ profileId: 'console-preview', profileVersion: 1, direction: 'forward', value: { type: 'number', number: 68 }, steps: [{ index: 0, transform: 'scale', input: { type: 'number', number: 20 }, output: { type: 'number', number: 68 } }] })
    render(<MappingPreview runPreview={runPreview} loadProfiles={async () => []} />)
    await userEvent.click(screen.getByRole('button', { name: '运行预览' }))
    expect(runPreview).toHaveBeenCalledWith(expect.objectContaining({ direction: 'forward', value: { type: 'number', number: 20 }, profile: expect.objectContaining({ schemaVersion: 1, inputType: 'number', transforms: [{ type: 'scale', factor: 1.8, offset: 32 }] }) }))
    expect(await screen.findByText('68')).toBeInTheDocument()
    expect(screen.getByText('20 → 68')).toBeInTheDocument()
  })

  it('builds reverse enum previews', async () => {
    const runPreview = vi.fn().mockResolvedValue({ profileId: 'console-preview', profileVersion: 1, direction: 'reverse', value: { type: 'enum', string: 'on' }, steps: [] })
    render(<MappingPreview runPreview={runPreview} loadProfiles={async () => []} />)
    await userEvent.selectOptions(screen.getByLabelText('转换类型'), 'enum')
    await userEvent.selectOptions(screen.getByLabelText('映射方向'), 'reverse')
    await userEvent.clear(screen.getByLabelText('预览输入值')); await userEvent.type(screen.getByLabelText('预览输入值'), 'active')
    await userEvent.click(screen.getByRole('button', { name: '运行预览' }))
    expect(runPreview).toHaveBeenCalledWith(expect.objectContaining({ direction: 'reverse', value: { type: 'enum', string: 'active' } }))
    expect(await screen.findByText('on')).toBeInTheDocument()
  })

	it('previews a hot-loaded database profile by id', async () => {
		const runPreview = vi.fn().mockResolvedValue({ profileId: 'saved-map', profileVersion: 3, direction: 'forward', value: { type: 'bool', bool: false }, steps: [] })
		render(<MappingPreview runPreview={runPreview} loadProfiles={async () => [{ schemaVersion: 1, id: 'saved-map', version: 3, kind: 'provider', inputType: 'bool', outputType: 'bool', transforms: [{ type: 'invert' }], builtIn: false }]} />)
		await userEvent.selectOptions(await screen.findByLabelText('预览 Profile'), 'saved-map')
		await userEvent.click(screen.getByRole('button', { name: '运行预览' }))
		expect(runPreview).toHaveBeenCalledWith({ profileId: 'saved-map', direction: 'forward', value: { type: 'bool', bool: true } })
		expect(screen.getByText(/最终输出.*bool/).parentElement).toHaveTextContent('false')
	})
})
