import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ProviderForm } from './ProviderForm'
import { ApiError } from '../api/client'

const xiaomiAPI = vi.hoisted(() => ({
	startXiaomiOAuth: vi.fn(),
	completeXiaomiOAuth: vi.fn(),
	discoverXiaomiGateways: vi.fn(),
}))

vi.mock('../api/xiaomi', () => xiaomiAPI)

describe('ProviderForm', () => {
	beforeEach(() => {
		vi.clearAllMocks()
		xiaomiAPI.startXiaomiOAuth.mockResolvedValue({ authorizationUrl: 'https://account.xiaomi.com/oauth2/authorize', state: 'expected-state', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321' })
		xiaomiAPI.completeXiaomiOAuth.mockResolvedValue({ oauth: { clientId: '1234567890', region: 'cn', redirectUrl: 'http://homeassistant.local:8123', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321' }, clientId: '987654321', caCertificate: 'ca', clientCertificate: 'certificate', privateKey: 'private-key' })
		xiaomiAPI.discoverXiaomiGateways.mockResolvedValue([])
	})

	afterEach(() => vi.restoreAllMocks())

  it('rejects non-object config before saving', async () => {
    const onSave = vi.fn(); const { container } = render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
    const editor = container.querySelector('textarea'); expect(editor).not.toBeNull(); fireEvent.change(editor!, { target: { value: '[]' } }); await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
    expect(screen.getByText('扩展配置必须是 JSON 对象')).toBeInTheDocument(); expect(onSave).not.toHaveBeenCalled()
  })

  it('loads one complete example for every model', async () => {
    const { container } = render(<ProviderForm provider={null} onCancel={() => {}} onSave={vi.fn()} />); await userEvent.click(screen.getByRole('button', { name: '载入完整模型示例' })); const editor = container.querySelector('textarea') as HTMLTextAreaElement
    const parsed = JSON.parse(editor.value) as { devices: Array<{ type: string }> }
    expect(new Set(parsed.devices.map((item) => item.type)).size).toBe(10); expect(editor.value).toContain('"airQuality": "good"'); expect(editor.value).toContain('"obstruction": false')
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

	it('builds a structured MQTT provider configuration', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'mqtt')
		const broker = screen.getByLabelText('MQTT Broker URL')
		await userEvent.clear(broker)
		await userEvent.type(broker, 'mqtt://broker.local:1883')
		await userEvent.type(screen.getByLabelText('MQTT 用户名'), 'homeloom')
		await userEvent.selectOptions(screen.getByLabelText('MQTT QoS'), '2')
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'mqtt', config: expect.objectContaining({ brokerUrl: 'mqtt://broker.local:1883', username: 'homeloom', qos: 2, topicPrefix: 'homeloom' }) }), false)
	})

	it('tests an MQTT connection without saving', async () => {
		const onTest = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={vi.fn()} onTest={onTest} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'mqtt')
		await userEvent.click(screen.getByRole('button', { name: '测试连接' }))
		await waitFor(() => expect(onTest).toHaveBeenCalledWith(expect.objectContaining({ type: 'mqtt', config: expect.objectContaining({ brokerUrl: 'mqtt://127.0.0.1:1883' }) })))
		expect(screen.getByText('连接成功，订阅已建立。')).toBeInTheDocument()
	})

	it('keeps subdevice mapping out of the Xiaomi connection form', async () => {
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={vi.fn()} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'xiaomi')
		await userEvent.type(screen.getByLabelText('小米 OAuth Client ID'), '1234567890')
		expect(screen.getByRole('button', { name: '打开小米授权页面' })).toBeInTheDocument()
		expect(screen.getByLabelText('小米 OAuth Redirect URL')).toHaveValue('http://homeassistant.local:8123')
		expect(screen.getByLabelText('小米 OAuth Redirect URL')).toHaveAttribute('readonly')
		expect(screen.getByLabelText('小米中枢网关地址')).toBeDisabled()
		expect(screen.queryByLabelText('小米设备映射')).not.toBeInTheDocument()
		expect(screen.getByText(/独立的“管理子设备”页面/)).toBeInTheDocument()
	})

	it('completes Xiaomi OAuth from a pasted callback URL', async () => {
		const popup = { location: { href: '' }, close: vi.fn() } as unknown as Window
		vi.spyOn(window, 'open').mockReturnValue(popup)
		render(<ProviderForm provider={null} initialType="xiaomi" onCancel={() => {}} onSave={vi.fn()} />)
		await userEvent.type(screen.getByLabelText('小米 OAuth Client ID'), '1234567890')
		await userEvent.click(screen.getByRole('button', { name: '打开小米授权页面' }))
		await waitFor(() => expect(xiaomiAPI.startXiaomiOAuth).toHaveBeenCalledWith(expect.objectContaining({ clientId: '1234567890', redirectUrl: 'http://homeassistant.local:8123' })))
		expect(popup.location.href).toBe('https://account.xiaomi.com/oauth2/authorize')
		expect(screen.getByText('授权页面已打开。完成授权后，请复制浏览器地址栏中的完整 URL 并粘贴到下方。')).toBeInTheDocument()

		await userEvent.type(screen.getByLabelText('小米 OAuth 回调 URL'), 'http://homeassistant.local:8123/?code=authorization-code&state=expected-state')
		await userEvent.click(screen.getByRole('button', { name: '解析 URL 并完成授权' }))
		await waitFor(() => expect(xiaomiAPI.completeXiaomiOAuth).toHaveBeenCalledWith({ clientId: '1234567890', region: 'cn', redirectUrl: 'http://homeassistant.local:8123', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321', code: 'authorization-code', state: 'expected-state' }))
		expect(screen.getByText(/OAuth 与中枢客户端证书已就绪/)).toBeInTheDocument()
	})

	it('rejects a callback URL from another origin', async () => {
		render(<ProviderForm provider={null} initialType="xiaomi" onCancel={() => {}} onSave={vi.fn()} />)
		await userEvent.type(screen.getByLabelText('小米 OAuth 回调 URL'), 'https://example.com/?code=code&state=state')
		await userEvent.click(screen.getByRole('button', { name: '解析 URL 并完成授权' }))
		expect(await screen.findByText('回调 URL 必须以 http://homeassistant.local:8123 开头')).toBeInTheDocument()
		expect(xiaomiAPI.completeXiaomiOAuth).not.toHaveBeenCalled()
	})

	it('opens directly in Xiaomi mode from the Xiaomi page', () => {
		render(<ProviderForm provider={null} initialType="xiaomi" onCancel={() => {}} onSave={vi.fn()} />)
		expect(screen.getByLabelText('类型')).toHaveValue('xiaomi')
		expect(screen.getByLabelText('小米中枢网关地址')).toBeInTheDocument()
	})
})
