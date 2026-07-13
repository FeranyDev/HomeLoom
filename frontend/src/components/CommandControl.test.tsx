import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { CommandControl } from './CommandControl'

describe('CommandControl', () => {
  it('builds typed command parameters', async () => {
    const onExecute = vi.fn().mockResolvedValue(undefined)
    render(<CommandControl definition={{ id: 'set-power', name: '设置开关', parameters: [{ id: 'value', name: '开关值', type: 'bool', required: true }] }} onExecute={onExecute} />)
    await userEvent.selectOptions(screen.getByRole('combobox'), 'true')
    await userEvent.click(screen.getByRole('button', { name: '执行命令' }))
    expect(onExecute).toHaveBeenCalledWith({ value: { type: 'bool', bool: true } })
  })
})
