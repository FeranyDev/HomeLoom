import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { MappingPreview } from './MappingPreview'

describe('MappingPreview', () => {
  it('submits a typed scale profile and explains the result', async () => {
    const runPreview = vi.fn().mockResolvedValue({ profileId: 'console-preview', profileVersion: 1, direction: 'forward', value: { type: 'number', number: 68 }, steps: [{ index: 0, transform: 'scale', input: { type: 'number', number: 20 }, output: { type: 'number', number: 68 } }] })
    render(<MappingPreview runPreview={runPreview} loadProfiles={async () => []} />)
    await userEvent.hover(screen.getByRole('button', { name: '映射预览说明' }))
    expect(screen.getByRole('tooltip')).toHaveTextContent('输入不会保存')
    await userEvent.click(screen.getByRole('button', { name: '运行预览' }))
    expect(runPreview).toHaveBeenCalledWith(expect.objectContaining({ direction: 'forward', value: { type: 'number', number: 20 }, profile: expect.objectContaining({ schemaVersion: 1, inputType: 'number', transforms: [{ type: 'scale', factor: 1.8, offset: 32 }] }) }))
    expect(await screen.findByText('68')).toBeInTheDocument()
    expect(screen.getByText('20 → 68')).toBeInTheDocument()
  })

  it('builds a reciprocal preview without extra parameters', async () => {
    const runPreview = vi.fn().mockResolvedValue({ profileId: 'console-preview', profileVersion: 1, direction: 'forward', value: { type: 'number', number: 0.25 }, steps: [{ index: 0, transform: 'reciprocal', input: { type: 'number', number: 4 }, output: { type: 'number', number: 0.25 } }] })
    render(<MappingPreview runPreview={runPreview} loadProfiles={async () => []} />)
    await userEvent.selectOptions(screen.getByLabelText('转换类型'), 'reciprocal')
    await userEvent.clear(screen.getByLabelText('预览输入值'))
    await userEvent.type(screen.getByLabelText('预览输入值'), '4')
    await userEvent.click(screen.getByRole('button', { name: '运行预览' }))

    expect(runPreview).toHaveBeenCalledWith(expect.objectContaining({ value: { type: 'number', number: 4 }, profile: expect.objectContaining({ inputType: 'number', outputType: 'number', transforms: [{ type: 'reciprocal' }] }) }))
    expect(await screen.findByText('4 → 0.25')).toBeInTheDocument()
  })

  it('builds int-number previews with direction-specific input types', async () => {
    const runPreview = vi.fn()
      .mockResolvedValueOnce({ profileId: 'console-preview', profileVersion: 1, direction: 'forward', value: { type: 'number', number: 20 }, steps: [] })
      .mockResolvedValueOnce({ profileId: 'console-preview', profileVersion: 1, direction: 'reverse', value: { type: 'int', int: 20 }, steps: [] })
    render(<MappingPreview runPreview={runPreview} loadProfiles={async () => []} />)

    await userEvent.selectOptions(screen.getByLabelText('转换类型'), 'int-number')
    expect(screen.getByText(/输入值.*整数（int）/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '运行预览' }))
    expect(runPreview).toHaveBeenLastCalledWith(expect.objectContaining({
      direction: 'forward', value: { type: 'int', int: 20 },
      profile: expect.objectContaining({ inputType: 'int', outputType: 'number', transforms: [{ type: 'int-number' }] }),
    }))

    await userEvent.selectOptions(screen.getByLabelText('映射方向'), 'reverse')
    expect(screen.getByText(/输入值.*数值（number）/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '运行预览' }))
    expect(runPreview).toHaveBeenLastCalledWith(expect.objectContaining({ direction: 'reverse', value: { type: 'number', number: 20 } }))
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

	it('changes the saved-profile input control with the active direction type', async () => {
		const runPreview = vi.fn().mockResolvedValue({ profileId: 'threshold-map', profileVersion: 2, direction: 'forward', value: { type: 'bool', bool: true }, steps: [] })
		render(<MappingPreview runPreview={runPreview} loadProfiles={async () => [{ schemaVersion: 1, id: 'threshold-map', version: 2, kind: 'capability', inputType: 'number', outputType: 'bool', transforms: [{ type: 'threshold', threshold: 50 }], builtIn: false }]} />)
		await userEvent.selectOptions(await screen.findByLabelText('预览 Profile'), 'threshold-map')

		expect(screen.getByText(/输入值.*数值（number）/)).toBeInTheDocument()
		expect(screen.getByLabelText('预览输入值')).toHaveAttribute('type', 'number')
		expect(screen.getByLabelText('预览输入值')).toHaveValue(20)
		await userEvent.click(screen.getByRole('button', { name: '运行预览' }))
		expect(runPreview).toHaveBeenLastCalledWith({ profileId: 'threshold-map', direction: 'forward', value: { type: 'number', number: 20 } })

		await userEvent.selectOptions(screen.getByLabelText('映射方向'), 'reverse')
		expect(screen.getByText(/输入值.*布尔值（bool）/)).toBeInTheDocument()
		expect(screen.getByLabelText('预览输入值').tagName).toBe('SELECT')
		expect(screen.getByLabelText('预览输入值')).toHaveValue('true')
	})

	it('offers known enum values for the selected saved profile', async () => {
		render(<MappingPreview runPreview={vi.fn()} loadProfiles={async () => [{ schemaVersion: 1, id: 'mode-map', version: 1, kind: 'provider', inputType: 'enum', outputType: 'enum', transforms: [{ type: 'enum', values: { off: 'inactive', on: 'active' } }], builtIn: false }]} />)
		await userEvent.selectOptions(await screen.findByLabelText('预览 Profile'), 'mode-map')
		expect(screen.getByRole('option', { name: 'off' })).toBeInTheDocument()
		expect(screen.getByRole('option', { name: 'on' })).toBeInTheDocument()
		await userEvent.selectOptions(screen.getByLabelText('映射方向'), 'reverse')
		expect(screen.getByRole('option', { name: 'inactive' })).toBeInTheDocument()
		expect(screen.getByRole('option', { name: 'active' })).toBeInTheDocument()
	})

	it('offers reverse-only enum aliases while previewing a saved profile', async () => {
		render(<MappingPreview runPreview={vi.fn()} loadProfiles={async () => [{ schemaVersion: 1, id: '018cc251-f400-7000-8000-000000000001', identifier: 'fan-mode', version: 1, kind: 'provider', inputType: 'enum', outputType: 'enum', transforms: [{ type: 'enum', values: { auto: 'auto' }, reverseValues: { cool: 'auto', heat: 'auto' } }], builtIn: false }]} />)
		await userEvent.selectOptions(await screen.findByLabelText('预览 Profile'), '018cc251-f400-7000-8000-000000000001')
		expect(screen.getByRole('option', { name: /fan-mode/ })).toBeInTheDocument()
		await userEvent.selectOptions(screen.getByLabelText('映射方向'), 'reverse')
		expect(screen.getByRole('option', { name: 'cool' })).toBeInTheDocument()
		expect(screen.getByRole('option', { name: 'heat' })).toBeInTheDocument()
	})

	it('uses enum-number bands as selectable incoming enum values', async () => {
		render(<MappingPreview runPreview={vi.fn()} loadProfiles={async () => [{ schemaVersion: 1, id: 'fan-map', version: 1, kind: 'target', inputType: 'enum', outputType: 'number', transforms: [{ type: 'enum-number', bands: [{ value: 'low', reverse: 25 }, { value: 'high', reverse: 100 }] }], builtIn: false }]} />)
		await userEvent.selectOptions(await screen.findByLabelText('预览 Profile'), 'fan-map')
		expect(screen.getByRole('option', { name: 'low' })).toBeInTheDocument()
		expect(screen.getByRole('option', { name: 'high' })).toBeInTheDocument()
	})
})
