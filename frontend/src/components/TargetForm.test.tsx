import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { TargetForm } from './TargetForm'

describe('TargetForm', () => {
  it('allows generated fields and selected device bindings', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined)
    render(<TargetForm target={null} devices={[{
      id: 'switch-1', providerId: 'virtual', name: '客厅开关', type: 'switch', online: true,
      state: { power: false }, lastUpdateAt: new Date().toISOString(),
    }]} onCancel={vi.fn()} onSave={onSave} />)
    await userEvent.click(screen.getByText('客厅开关'))
    await userEvent.click(screen.getByRole('button', { name: '保存到数据库' }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      id: '', address: '', pin: '', setupId: '', deviceIds: ['switch-1'],
    }), false)
  })
})
