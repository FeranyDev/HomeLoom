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
      id: '', type: 'apple-hap', config: { address: '', pin: '', setupId: '' }, deviceIds: [], devices: [],
    }), false)
  })

  it('only shows protocol-specific fields for the selected Target adapter', async () => {
    render(<TargetForm target={null} onCancel={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByLabelText(/HomeKit 设置标识/)).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText(/目标类型/), 'matter')
    expect(screen.queryByLabelText(/HomeKit 设置标识/)).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/HAP 监听地址/)).not.toBeInTheDocument()
    expect(screen.getByLabelText(/网络接口/)).toBeInTheDocument()
    expect(screen.getByLabelText(/UDP 监听端口/)).toBeInTheDocument()
    expect(screen.getByLabelText(/默认配网窗口时长/)).toBeInTheDocument()
    expect(screen.getByText('留空不是 HomeKit 回退')).toBeInTheDocument()
  })

  it('shows server field errors and refills an existing target', async () => {
		const target: Target = { id: 'apple-main', type: 'apple-hap', name: 'Main Bridge', enabled: true, status: 'running', config: { address: ':51826', setupId: 'HLM1' }, pairing: { pairingCode: '001-02-003', paired: false }, deviceIds: ['switch-1'], devices: [] }
		const onSave = vi.fn().mockRejectedValue(new ApiError('invalid target configuration', 400, { pin: 'must contain 8 digits' }))
		render(<TargetForm target={target} onCancel={vi.fn()} onSave={onSave} />)
		expect(screen.getByDisplayValue('Main Bridge')).toBeInTheDocument(); expect(screen.getByDisplayValue(':51826')).toBeInTheDocument(); expect(screen.getByDisplayValue('HLM1')).toBeInTheDocument(); expect(screen.getByDisplayValue('00102003')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
		expect(await screen.findByText('must contain 8 digits')).toBeInTheDocument(); expect(screen.getByDisplayValue('00102003')).toHaveAttribute('aria-invalid', 'true')
	})

	it('hides one-time HomeKit parameters when editing a paired bridge', async () => {
		const target: Target = { id: 'apple-main', type: 'apple-hap', name: 'Main Bridge', enabled: true, status: 'running', config: { address: ':51826' }, pairing: { paired: true }, deviceIds: [], devices: [] }
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetForm target={target} onCancel={vi.fn()} onSave={onSave} />)
		expect(screen.getByText('一次性配对参数已隐藏并锁定')).toBeInTheDocument()
		expect(screen.getByLabelText(/目标类型/)).toBeDisabled()
		expect(screen.queryByLabelText(/HomeKit 设置标识/)).not.toBeInTheDocument()
		expect(screen.queryByLabelText(/HomeKit 8 位配对码/)).not.toBeInTheDocument()
		expect(screen.getByLabelText(/HAP 监听地址/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ id: target.id, config: { address: ':51826', pin: '', setupId: '' } }), true)
	})

	it('uses explicit Matter automatic null values rather than HomeKit fields', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<TargetForm target={null} onCancel={vi.fn()} onSave={onSave} />)
		await userEvent.type(screen.getByLabelText(/^名称/), '客厅桥')
		await userEvent.selectOptions(screen.getByLabelText(/目标类型/), 'matter')
		expect(screen.getByDisplayValue('客厅桥')).toBeInTheDocument()
		await userEvent.type(screen.getByLabelText(/网络接口/), 'en0')
		await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'matter', name: '客厅桥', config: expect.objectContaining({ networkInterface: 'en0', udpPort: null, discriminator: null, passcode: null, vendorId: null, productId: null, commissioningWindowSeconds: null }) }), false)
	})
})
