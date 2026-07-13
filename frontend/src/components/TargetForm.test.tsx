import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { TargetForm } from './TargetForm'
import { ApiError } from '../api/client'
import type { Target } from '../types/target'

describe('TargetForm', () => {
  it('allows generated fields and selected device bindings', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(<TargetForm target={null} devices={[{
      schemaVersion: 1, id: 'switch-1', providerId: 'virtual', name: '客厅开关', type: 'switch', online: true,
      endpoints: [], lastUpdateAt: new Date().toISOString(),
    }]} onCancel={vi.fn()} onSave={onSave} />)
    await userEvent.click(screen.getByText('客厅开关'))
    await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      id: '', address: '', pin: '', setupId: '', deviceIds: ['switch-1'],
    }), false)
  })

  it('shows server field errors and refills an existing target', async () => {
		const target: Target = { id: 'apple-main', type: 'apple-hap', name: 'Main Bridge', enabled: true, status: 'running', address: ':51826', setupId: 'HLM1', pairingCode: '001-02-003', deviceIds: ['switch-1'] }
		const onSave = vi.fn().mockRejectedValue(new ApiError('invalid target configuration', 400, { pin: 'must contain 8 digits' }))
		render(<TargetForm target={target} devices={[]} onCancel={vi.fn()} onSave={onSave} />)
		expect(screen.getByDisplayValue('Main Bridge')).toBeInTheDocument(); expect(screen.getByDisplayValue(':51826')).toBeInTheDocument(); expect(screen.getByDisplayValue('HLM1')).toBeInTheDocument(); expect(screen.getByDisplayValue('00102003')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
		expect(await screen.findByText('must contain 8 digits')).toBeInTheDocument(); expect(screen.getByDisplayValue('00102003')).toHaveAttribute('aria-invalid', 'true')
	})
})
