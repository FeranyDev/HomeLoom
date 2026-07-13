import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { PropertyControl } from './PropertyControl'

describe('PropertyControl', () => {
  it('writes typed boolean and number values', async () => {
    const writeBool = vi.fn().mockResolvedValue(undefined); const { unmount } = render(<PropertyControl definition={{ id: 'power', name: 'Power', type: 'bool', parameterLevel: 'required', readable: true, writable: true, notifiable: true }} value={{ type: 'bool', bool: false }} onWrite={writeBool} />)
    expect(screen.getByText('必须参数')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '设为 true' })); expect(writeBool).toHaveBeenCalledWith({ type: 'bool', bool: true }); unmount()
    const writeNumber = vi.fn().mockResolvedValue(undefined); render(<PropertyControl definition={{ id: 'level', name: 'Level', type: 'number', readable: true, writable: true, notifiable: true, min: 0, max: 100, step: 1 }} value={{ type: 'number', number: 20 }} onWrite={writeNumber} />)
    const input = screen.getByRole('spinbutton'); await userEvent.clear(input); await userEvent.type(input, '42'); await userEvent.click(screen.getByRole('button', { name: '写入属性' })); expect(writeNumber).toHaveBeenCalledWith({ type: 'number', number: 42 })
  })

	it('writes integers and rejects fractional input', async () => {
		const onWrite = vi.fn().mockResolvedValue(undefined)
		render(<PropertyControl definition={{ id: 'count', name: 'Count', type: 'int', readable: true, writable: true, notifiable: true }} value={{ type: 'int', int: 3 }} onWrite={onWrite} />)
		const input = screen.getByRole('spinbutton')
		await userEvent.clear(input); await userEvent.type(input, '4.5'); await userEvent.click(screen.getByRole('button', { name: '写入属性' }))
		expect(await screen.findByText('请输入有效整数')).toBeInTheDocument(); expect(onWrite).not.toHaveBeenCalled()
		await userEvent.clear(input); await userEvent.type(input, '5'); await userEvent.click(screen.getByRole('button', { name: '写入属性' }))
		expect(onWrite).toHaveBeenCalledWith({ type: 'int', int: 5 })
	})
})
