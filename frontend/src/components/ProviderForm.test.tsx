import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ProviderForm } from './ProviderForm'
import { ApiError } from '../api/client'

describe('ProviderForm', () => {
  it('rejects non-object config before saving', async () => {
    const onSave = vi.fn(); const { container } = render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
    const editor = container.querySelector('textarea'); expect(editor).not.toBeNull(); fireEvent.change(editor!, { target: { value: '[]' } }); await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
    expect(screen.getByText('扩展配置必须是 JSON 对象')).toBeInTheDocument(); expect(onSave).not.toHaveBeenCalled()
  })

  it('loads the sensor example', async () => {
    const { container } = render(<ProviderForm provider={null} onCancel={() => {}} onSave={vi.fn()} />); await userEvent.click(screen.getByRole('button', { name: '载入传感器示例' })); const editor = container.querySelector('textarea') as HTMLTextAreaElement
    expect(editor.value).toContain('"type": "humidity-sensor"'); expect(editor.value).toContain('"type": "contact-sensor"'); expect(editor.value).toContain('"type": "motion-sensor"')
  })

  it('shows server validation beside the matching field', async () => {
		const onSave = vi.fn().mockRejectedValue(new ApiError('invalid provider configuration', 400, { id: 'invalid id' }, 'bad_request', 'request-1'))
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(await screen.findByText('invalid id')).toBeInTheDocument(); expect(screen.getByLabelText(/ID/)).toHaveAttribute('aria-invalid', 'true')
	})

	it('explains redacted secret placeholders while editing', () => {
		render(<ProviderForm provider={{ id: 'secure', type: 'virtual', name: 'Secure', enabled: true, config: { password: '********' }, status: 'disabled', capabilities: { discovery: false, propertyRead: false, propertyWrite: false, events: false }, retryCount: 0 }} onCancel={() => {}} onSave={vi.fn()} />)
		expect(screen.getByText(/保持占位符即可沿用数据库中的原值/)).toBeInTheDocument()
	})
})
