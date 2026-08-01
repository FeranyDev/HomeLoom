import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ProviderForm } from './ProviderForm'
import { ApiError } from '../api/client'
import type { Provider } from '../types/provider'

const xiaomiAPI = vi.hoisted(() => ({
	startXiaomiOAuth: vi.fn(),
	completeXiaomiOAuth: vi.fn(),
	discoverXiaomiGateways: vi.fn(),
	startXiaomiCloudLogin: vi.fn(),
	verifyXiaomiCloudLogin: vi.fn(),
}))

vi.mock('../api/xiaomi', () => xiaomiAPI)

describe('ProviderForm', () => {
	beforeEach(() => {
		vi.clearAllMocks()
		xiaomiAPI.startXiaomiOAuth.mockResolvedValue({ authorizationUrl: 'https://account.xiaomi.com/oauth2/authorize', state: 'expected-state', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321' })
		xiaomiAPI.completeXiaomiOAuth.mockResolvedValue({ oauth: { clientId: '1234567890', region: 'cn', redirectUrl: 'http://homeassistant.local:8123', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321' }, clientId: '987654321', caCertificate: 'ca', clientCertificate: 'certificate', privateKey: 'private-key' })
		xiaomiAPI.discoverXiaomiGateways.mockResolvedValue([])
		xiaomiAPI.startXiaomiCloudLogin.mockResolvedValue({ status: 'verified', userId: '42', ssecurity: 'security', serviceToken: 'service-token', passToken: 'camera-pass-token' })
		xiaomiAPI.verifyXiaomiCloudLogin.mockResolvedValue({ status: 'verified', userId: '42', ssecurity: 'security', serviceToken: 'service-token', passToken: 'camera-pass-token' })
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
		 expect(new Set(parsed.devices.map((item) => item.type)).size).toBe(37); expect(editor.value).toContain('"temperature-sensor"'); expect(editor.value).toContain('"humidity-sensor"'); expect(editor.value).toContain('"temperature-humidity-sensor"'); expect(editor.value).toContain('"power-meter"'); expect(editor.value).toContain('"ev-charger"'); expect(editor.value).toContain('"air-conditioner"'); expect(editor.value).toContain('"television"'); expect(editor.value).toContain('"robot-vacuum"'); expect(editor.value).toContain('"airQuality": "good"'); expect(editor.value).toContain('"obstruction": false')
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
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'mqtt-client')
		const broker = screen.getByLabelText('MQTT Broker URL')
		await userEvent.clear(broker)
		await userEvent.type(broker, 'mqtt://broker.local:1883')
		await userEvent.type(screen.getByLabelText('MQTT 用户名'), 'homeloom')
		expect(screen.queryByLabelText('MQTT Topic Prefix')).not.toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'mqtt', config: expect.objectContaining({ mode: 'client', brokerUrl: 'mqtt://broker.local:1883', username: 'homeloom', devices: [] }) }), false)
	})

  it('creates an independent camera provider without selecting an output target', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'camera')
		expect(screen.getByText('创建 Camera Provider')).toBeInTheDocument()
		expect(screen.getByText(/管理摄像头/)).toBeInTheDocument()
		expect(screen.queryByLabelText('摄像头 ID')).not.toBeInTheDocument()
		await userEvent.type(screen.getByLabelText(/ID/), 'camera-main')
		await userEvent.type(screen.getByLabelText('名称'), '家庭摄像头')
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			type: 'camera',
			config: expect.objectContaining({ cameras: [] }),
		}), false)
  })

	it('creates a Virtual Provider with an explicit empty child-device catalog', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
		await userEvent.type(screen.getByLabelText(/ID/), 'virtual-lab')
		await userEvent.type(screen.getByLabelText('名称'), '实验室虚拟设备')
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'virtual', config: expect.objectContaining({ devices: [] }) }), false)
	})

	it('tests an MQTT connection without saving', async () => {
		const onTest = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={vi.fn()} onTest={onTest} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'mqtt-client')
		await userEvent.click(screen.getByRole('button', { name: '测试连接' }))
		await waitFor(() => expect(onTest).toHaveBeenCalledWith(expect.objectContaining({ type: 'mqtt', config: expect.objectContaining({ mode: 'client', brokerUrl: 'mqtt://127.0.0.1:1883' }) })))
		expect(screen.getByText('MQTT 客户端已连接外部 Broker。保存 Provider 后再配置设备 Topic。')).toBeInTheDocument()
	})

	it('exposes MQTT client and server as top-level provider choices without a second mode selector', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const onTest = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="mqtt-server" onCancel={() => {}} onSave={onSave} onTest={onTest} />)
		expect(screen.getByRole('option', { name: /MQTT Client/ })).toBeInTheDocument()
		expect(screen.getByRole('option', { name: /MQTT Server/ })).toBeInTheDocument()
		expect(screen.queryByLabelText('MQTT 运行模式')).not.toBeInTheDocument()
		expect(screen.getByText('MQTT Server · 服务端')).toBeInTheDocument()
		expect(screen.queryByLabelText('MQTT Broker URL')).not.toBeInTheDocument()
		const listen = screen.getByLabelText('MQTT 服务端监听地址')
		await userEvent.clear(listen)
		await userEvent.type(listen, '0.0.0.0:1883')
		await userEvent.type(screen.getByLabelText('MQTT 用户名'), 'device')
		await userEvent.type(screen.getByLabelText('MQTT 密码'), 'secret')
		await userEvent.click(screen.getByRole('button', { name: '测试监听' }))
		await waitFor(() => expect(onTest).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ mode: 'server', listenAddress: '0.0.0.0:1883' }) })))
		expect(screen.getByText(/MQTT 服务端监听测试成功/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'mqtt', config: expect.objectContaining({ mode: 'server', listenAddress: '0.0.0.0:1883', username: 'device', password: 'secret', devices: [] }) }), false)
	})

	it('keeps subdevice mapping out of the Xiaomi connection form', async () => {
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={vi.fn()} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'xiaomi')
		await userEvent.type(screen.getByLabelText('小米 OAuth Client ID'), '1234567890')
		expect(screen.getByRole('button', { name: '打开小米授权页面' })).toBeInTheDocument()
		expect(screen.getByLabelText('小米 OAuth Redirect URL')).toHaveValue('http://homeassistant.local:8123')
		expect(screen.getByLabelText('小米 OAuth Redirect URL')).toHaveAttribute('readonly')
		expect(screen.getByLabelText('小米中枢网关地址')).toBeDisabled()
		expect(screen.getByRole('button', { name: '发现小米中枢网关' })).toBeDisabled()
		expect(screen.queryByLabelText('小米设备映射')).not.toBeInTheDocument()
		expect(screen.getByText(/独立的“管理子设备”页面/)).toBeInTheDocument()
	})

	it('discovers and selects a gateway only inside the Xiaomi central hub configuration', async () => {
		xiaomiAPI.discoverXiaomiGateways.mockResolvedValue([{ instance: 'central-hub', hostName: 'hub.local', addresses: ['192.168.1.50'], port: 8883, did: 'hub-did', role: 1, mqttEnabled: true }])
		const provider: Provider = { id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', enabled: true, status: 'running', retryCount: 0, capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: true }, config: { host: '', port: 8883, clientId: '987654321', caCertificate: 'ca', clientCertificate: 'certificate', privateKey: 'private-key', oauth: { clientId: '1234567890', region: 'cn', redirectUrl: 'http://homeassistant.local:8123', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321' }, devices: [] } }
		render(<ProviderForm provider={provider} onCancel={() => {}} onSave={vi.fn()} />)
		await userEvent.click(screen.getByRole('button', { name: '发现小米中枢网关' }))
		await waitFor(() => expect(xiaomiAPI.discoverXiaomiGateways).toHaveBeenCalledOnce())
		const candidate = await screen.findByRole('button', { name: /central-hub.*192\.168\.1\.50/ })
		await userEvent.click(candidate)
		expect(screen.getByLabelText('小米中枢网关地址')).toHaveValue('192.168.1.50')
		expect(screen.getByLabelText('小米中枢网关端口')).toHaveValue(8883)
	})

	it('builds a distinctly labelled third-party MIoT cloud provider', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'xiaomi-miot-cloud')
		await userEvent.type(screen.getByLabelText('小米 MIoT 云账号'), 'owner@example.com')
		await userEvent.type(screen.getByLabelText('小米 MIoT 云密码'), 'account-password')
		expect(screen.getByText(/并非预留的官方 Xiaomi Home Cloud Provider/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '登录小米云账号' }))
		await waitFor(() => expect(xiaomiAPI.startXiaomiCloudLogin).toHaveBeenCalledWith({ region: 'cn', username: 'owner@example.com', password: 'account-password', requestTimeoutSeconds: 15 }))
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
			expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'xiaomi-miot-cloud', config: expect.objectContaining({ region: 'cn', username: 'owner@example.com', password: 'account-password', userId: '42', ssecurity: 'security', serviceToken: 'service-token', passToken: 'camera-pass-token', pollIntervalSeconds: 30, devices: [] }) }), false)
	})

	it('guides Xiaomi cloud SMS verification and resumes the pending login', async () => {
		xiaomiAPI.startXiaomiCloudLogin.mockResolvedValue({ status: 'verification_required', challengeId: 'challenge-1', verificationUrl: 'https://account.xiaomi.com/fe/service/identity/authStart', expiresAt: '2030-01-01T00:00:00Z' })
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="xiaomi-miot-cloud" onCancel={() => {}} onSave={onSave} />)
		await userEvent.type(screen.getByLabelText('小米 MIoT 云账号'), 'owner@example.com')
		await userEvent.type(screen.getByLabelText('小米 MIoT 云密码'), 'account-password')
		await userEvent.click(screen.getByRole('button', { name: '登录小米云账号' }))
		expect(await screen.findByRole('link', { name: '打开小米身份验证页面' })).toHaveAttribute('href', 'https://account.xiaomi.com/fe/service/identity/authStart')
		expect(screen.getByText(/收到后不要在小米页面提交/)).toBeInTheDocument()
		await userEvent.type(screen.getByLabelText('小米 MIoT 云验证码'), '123456')
		await userEvent.click(screen.getByRole('button', { name: '提交验证码并继续登录' }))
		await waitFor(() => expect(xiaomiAPI.verifyXiaomiCloudLogin).toHaveBeenCalledWith({ challengeId: 'challenge-1', code: '123456' }))
		expect(screen.getByText(/云会话已就绪/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
			expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ userId: '42', serviceToken: 'service-token', passToken: 'camera-pass-token' }) }), false)
		})

		it('requires a password-login passToken only when Xiaomi MISS cameras are configured', async () => {
			const onSave = vi.fn().mockResolvedValue(undefined)
			const cloud: Provider = {
				id: 'xiaomi-miot-cloud-main',
				type: 'xiaomi-miot-cloud',
				name: '小米 MIoT 云',
				enabled: true,
				status: 'running',
				retryCount: 0,
				capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: false },
				config: {
					region: 'cn',
					userId: '42',
					ssecurity: '********',
					serviceToken: '********',
					devices: [{ did: 'camera-1', id: 'xiaomi-miot-camera-1', name: 'Camera', type: 'camera', media: { protocol: 'xiaomi-miss' } }],
				},
			}
			render(<ProviderForm provider={cloud} onCancel={() => {}} onSave={onSave} />)
			expect(screen.getByText(/摄像头还需要使用账号密码重新登录/)).toBeInTheDocument()
			await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
			expect(screen.getByText(/取得摄像头所需的 passToken/)).toBeInTheDocument()
			expect(onSave).not.toHaveBeenCalled()

			const advanced = screen.getByText('已有会话凭据（高级替代方案）')
			await userEvent.click(advanced)
			const input = screen.getByLabelText('小米 MIoT 云 Camera Pass Token')
			expect(input).toHaveAttribute('type', 'password')
			await userEvent.type(input, 'camera-pass-token')
			await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
			await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
				config: expect.objectContaining({ passToken: 'camera-pass-token' }),
			}), true))
		})

	it('keeps the original three-field MIoT session valid for ordinary devices', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const cloud: Provider = {
			id: 'xiaomi-miot-cloud-main',
			type: 'xiaomi-miot-cloud',
			name: '小米 MIoT 云',
			enabled: true,
			status: 'running',
			retryCount: 0,
			capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: false },
			config: {
				region: 'cn',
				userId: '42',
				ssecurity: '********',
				serviceToken: '********',
				devices: [{ did: 'light-1', id: 'xiaomi-miot-light-1', name: 'Light', type: 'lightbulb' }],
			},
		}
		render(<ProviderForm provider={cloud} onCancel={() => {}} onSave={onSave} />)
		expect(screen.getByText(/云会话已就绪/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			config: expect.not.objectContaining({ passToken: expect.anything() }),
		}), true))
	})

	it('does not save a Xiaomi cloud provider before account login is complete', async () => {
		const onSave = vi.fn()
		render(<ProviderForm provider={null} initialType="xiaomi-miot-cloud" onCancel={() => {}} onSave={onSave} />)
		await userEvent.type(screen.getByLabelText('小米 MIoT 云账号'), 'owner@example.com')
		await userEvent.type(screen.getByLabelText('小米 MIoT 云密码'), 'account-password')
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(screen.getByText(/请先完成“小米云账号登录”/)).toBeInTheDocument()
		expect(onSave).not.toHaveBeenCalled()
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
	}, 15_000)

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
