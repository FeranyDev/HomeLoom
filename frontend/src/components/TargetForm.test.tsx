import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { TargetForm } from './TargetForm'
import { ApiError } from '../api/client'
import type { Target } from '../types/target'

describe('TargetForm', () => {
  it('creates target configuration without embedding Consumer devices', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(<TargetForm target={null} onCancel={vi.fn()} onSave={onSave} />)
    expect(screen.getByText('保存目标实例后单独配置')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      id: '', address: '', pin: '', setupId: '', deviceIds: [], devices: [],
    }), false)
  })

  it('only shows protocol-specific fields for the selected Target adapter', async () => {
    render(<TargetForm target={null} onCancel={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByLabelText(/HomeKit 设置标识/)).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText(/目标类型/), 'matter')
    expect(screen.queryByLabelText(/HomeKit 设置标识/)).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/HAP 监听地址/)).not.toBeInTheDocument()
    expect(screen.getByText(/matter.*尚未实现运行时/i)).toBeInTheDocument()
  })

  it('shows server field errors and refills an existing target', async () => {
		const target: Target = { id: 'apple-main', type: 'apple-hap', name: 'Main Bridge', enabled: true, status: 'running', address: ':51826', setupId: 'HLM1', pairingCode: '001-02-003', paired: false, deviceIds: ['switch-1'], devices: [] }
		const onSave = vi.fn().mockRejectedValue(new ApiError('invalid target configuration', 400, { pin: 'must contain 8 digits' }))
		render(<TargetForm target={target} onCancel={vi.fn()} onSave={onSave} />)
		expect(screen.getByDisplayValue('Main Bridge')).toBeInTheDocument(); expect(screen.getByDisplayValue(':51826')).toBeInTheDocument(); expect(screen.getByDisplayValue('HLM1')).toBeInTheDocument(); expect(screen.getByDisplayValue('00102003')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
		expect(await screen.findByText('must contain 8 digits')).toBeInTheDocument(); expect(screen.getByDisplayValue('00102003')).toHaveAttribute('aria-invalid', 'true')
	})

	it('hides one-time HomeKit parameters when editing a paired bridge', async () => {
		const target: Target = { id: 'apple-main', type: 'apple-hap', name: 'Main Bridge', enabled: true, status: 'running', address: ':51826', paired: true, deviceIds: [], devices: [] }
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetForm target={target} onCancel={vi.fn()} onSave={onSave} />)
		expect(screen.getByText('一次性配对参数已隐藏并锁定')).toBeInTheDocument()
		expect(screen.getByLabelText(/目标类型/)).toBeDisabled()
		expect(screen.queryByLabelText(/HomeKit 设置标识/)).not.toBeInTheDocument()
		expect(screen.queryByLabelText(/HomeKit 8 位配对码/)).not.toBeInTheDocument()
		expect(screen.getByLabelText(/HAP 监听地址/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ id: target.id, pin: '', setupId: '' }), true)
	})
})
