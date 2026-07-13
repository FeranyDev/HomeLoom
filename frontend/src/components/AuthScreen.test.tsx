import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { AuthScreen } from './AuthScreen'

describe('AuthScreen', () => {
	it('validates confirmation before initializing the administrator', async () => {
		const submit = vi.fn()
		render(<AuthScreen initialized={false} onSubmit={submit} />)
		const user = userEvent.setup()
		await user.clear(screen.getByLabelText('用户名'))
		await user.type(screen.getByLabelText('用户名'), 'owner')
		await user.type(screen.getByLabelText('密码'), 'a-long-password')
		await user.type(screen.getByLabelText('确认密码'), 'different-pass')
		await user.click(screen.getByRole('button', { name: '创建管理员' }))
		expect(screen.getByRole('alert')).toHaveTextContent('两次输入的密码不一致')
		expect(submit).not.toHaveBeenCalled()
	})

	it('submits a login without showing password confirmation', async () => {
		const submit = vi.fn().mockResolvedValue(undefined)
		render(<AuthScreen initialized onSubmit={submit} />)
		const user = userEvent.setup()
		await user.type(screen.getByLabelText('密码'), 'a-long-password')
		await user.click(screen.getByRole('button', { name: '登录' }))
		expect(screen.queryByLabelText('确认密码')).not.toBeInTheDocument()
		expect(submit).toHaveBeenCalledWith('admin', 'a-long-password')
	})
})
