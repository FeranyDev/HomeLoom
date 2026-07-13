import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { CommandControl } from './CommandControl'

describe('CommandControl', () => {
  it('builds typed command parameters', async () => {
    const onExecute = vi.fn().mockResolvedValue(undefined)
    render(<CommandControl definition={{ id: 'set-power', name: '设置开关', idempotent: true, parameters: [{ id: 'value', name: '开关值', type: 'bool', required: true }] }} onExecute={onExecute} />)
		expect(screen.getByText('可安全重放')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox'), 'true')
    await userEvent.click(screen.getByRole('button', { name: '执行命令' }))
		expect(onExecute).toHaveBeenCalledWith({ value: { type: 'bool', bool: true } }, expect.any(String))
  })

  it('reuses the idempotency key when retrying a failed request', async () => {
		const onExecute = vi.fn().mockRejectedValueOnce(new Error('network')).mockResolvedValueOnce(undefined)
		render(<CommandControl definition={{ id: 'toggle', name: '切换' }} onExecute={onExecute} />)
		expect(screen.getByText('禁止自动重试')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '执行命令' })); expect(await screen.findByText('network')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '执行命令' }))
		expect(onExecute.mock.calls[0][1]).toBe(onExecute.mock.calls[1][1])
	})
})
