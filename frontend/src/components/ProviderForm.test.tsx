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
	getXiaomiProviderAuthChallenge: vi.fn(),
	verifyXiaomiProviderAuthChallenge: vi.fn(),
}))

const providerAPI = vi.hoisted(() => ({
	scanProviderNetwork: vi.fn(),
}))

const tuyaAPI = vi.hoisted(() => ({
	startTuyaSharingLogin: vi.fn(),
	pollTuyaSharingLogin: vi.fn(),
	tuyaSharingQRCodeURL: (state: string) => `/api/v1/tuya/login/qr?state=${encodeURIComponent(state)}`,
	startTuyaOAuth: vi.fn(),
	completeTuyaOAuth: vi.fn(),
	tuyaOAuthQRCodeURL: (state: string) => `/api/v1/tuya/oauth/qr?state=${encodeURIComponent(state)}`,
	parseTuyaOAuthCallback: (value: unknown) => value && typeof value === 'object' && (value as Record<string, unknown>).type === 'homeloom-tuya-oauth' ? value : null,
}))

const sonoffAPI = vi.hoisted(() => ({
	loginSonoff: vi.fn(),
}))

vi.mock('../api/xiaomi', () => xiaomiAPI)
vi.mock('../api/providers', () => providerAPI)
vi.mock('../api/tuya', () => tuyaAPI)
vi.mock('../api/sonoff', () => sonoffAPI)

describe('ProviderForm', () => {
	beforeEach(() => {
		vi.clearAllMocks()
		xiaomiAPI.startXiaomiOAuth.mockResolvedValue({ authorizationUrl: 'https://account.xiaomi.com/oauth2/authorize', state: 'expected-state', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321' })
		xiaomiAPI.completeXiaomiOAuth.mockResolvedValue({ oauth: { clientId: '1234567890', region: 'cn', redirectUrl: 'http://homeassistant.local:8123', oauthUuid: '0123456789abcdef0123456789abcdef', virtualDid: '987654321' }, clientId: '987654321', caCertificate: 'ca', clientCertificate: 'certificate', privateKey: 'private-key' })
		xiaomiAPI.discoverXiaomiGateways.mockResolvedValue([])
		xiaomiAPI.startXiaomiCloudLogin.mockResolvedValue({ status: 'verified', userId: '42', ssecurity: 'security', serviceToken: 'service-token', passToken: 'camera-pass-token' })
		xiaomiAPI.verifyXiaomiCloudLogin.mockResolvedValue({ status: 'verified', userId: '42', ssecurity: 'security', serviceToken: 'service-token', passToken: 'camera-pass-token' })
		xiaomiAPI.getXiaomiProviderAuthChallenge.mockResolvedValue(null)
		xiaomiAPI.verifyXiaomiProviderAuthChallenge.mockResolvedValue({})
		providerAPI.scanProviderNetwork.mockResolvedValue([])
		tuyaAPI.startTuyaOAuth.mockResolvedValue({ authorizationUrl: 'https://auth.tuya.example/authorize?state=tuya-state', state: 'tuya-state', expiresAt: '2030-01-01T00:10:00Z' })
		tuyaAPI.completeTuyaOAuth.mockResolvedValue({ accessToken: 'tuya-access', refreshToken: 'tuya-refresh', uid: 'tuya-user', expiresAt: '2030-01-01T01:00:00Z' })
		tuyaAPI.startTuyaSharingLogin.mockResolvedValue({ state: 'sharing-state', qrData: 'tuyaSmart--qrLogin?token=qr-token', expiresAt: '2030-01-01T00:05:00Z' })
		tuyaAPI.pollTuyaSharingLogin.mockResolvedValue({ status: 'pending' })
		sonoffAPI.loginSonoff.mockResolvedValue({ accessToken: 'sonoff-access', region: 'cn', endpoint: 'https://cn-apia.coolkit.cn' })
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

	it('creates an empty Gree Provider and defers device setup to its manager', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} onCancel={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByLabelText('类型'), 'gree')
		expect(screen.getByRole('option', { name: 'Gree 局域网空调' })).toBeInTheDocument()
		expect(screen.queryByLabelText('Gree 设备地址（host）')).not.toBeInTheDocument()
		expect(screen.getByText('保存后管理格力设备')).toBeInTheDocument()
		await userEvent.clear(screen.getByLabelText('Gree 轮询间隔'))
		await userEvent.type(screen.getByLabelText('Gree 轮询间隔'), '45')
		await userEvent.clear(screen.getByLabelText('Gree 请求超时'))
		await userEvent.type(screen.getByLabelText('Gree 请求超时'), '8')
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))

		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			type: 'gree',
			config: { devices: [], pollIntervalSeconds: 45, requestTimeoutSeconds: 8 },
		}), false))
	})

	it('configures network reachability monitoring and Wake-on-LAN', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const onTest = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="network" onCancel={() => {}} onSave={onSave} onTest={onTest} />)
		expect(screen.getByRole('option', { name: '网络设备监测 / Wake-on-LAN' })).toBeInTheDocument()
		expect(screen.getByText('配置局域网设备监测')).toBeInTheDocument()
		expect(screen.getByLabelText('网络设备 1 ID')).toHaveValue('living-room-pc')
		expect(screen.getByLabelText('网络设备 1 Host')).toHaveValue('192.168.1.100')
		expect(screen.getByText('高级 JSON 导入 / 导出')).toBeInTheDocument()
		await userEvent.clear(screen.getByLabelText('网络设备探测间隔'))
		await userEvent.type(screen.getByLabelText('网络设备探测间隔'), '45')
		await userEvent.clear(screen.getByLabelText('网络设备探测超时'))
		await userEvent.type(screen.getByLabelText('网络设备探测超时'), '5')
		await userEvent.click(screen.getByRole('button', { name: '添加网络设备' }))
		await userEvent.clear(screen.getByLabelText('网络设备 2 ID'))
		await userEvent.type(screen.getByLabelText('网络设备 2 ID'), 'bedroom-nas')
		await userEvent.type(screen.getByLabelText('网络设备 2 名称'), '卧室 NAS')
		await userEvent.type(screen.getByLabelText('网络设备 2 Host'), '192.168.1.20')
		await userEvent.clear(screen.getByLabelText('网络设备 2 探测端口'))
		await userEvent.type(screen.getByLabelText('网络设备 2 探测端口'), '443')
		await userEvent.type(screen.getByLabelText('网络设备 2 MAC'), '11:22:33:44:55:66')
		await userEvent.click(screen.getAllByText('单项高级覆盖（可选）')[1])
		await userEvent.type(screen.getByLabelText('网络设备 2 离线阈值覆盖'), '3')
		await userEvent.click(screen.getByRole('button', { name: '移除网络设备 1' }))
		await userEvent.click(screen.getByRole('button', { name: '测试网络设备连接' }))
		await waitFor(() => expect(onTest).toHaveBeenCalledWith(expect.objectContaining({
			type: 'network',
			config: expect.objectContaining({
				probeIntervalSeconds: 45,
				probeTimeoutSeconds: 5,
				onlineThreshold: 1,
				offlineThreshold: 2,
				wolBroadcastAddress: '255.255.255.255',
				wolPort: 9,
				devices: [expect.objectContaining({ id: 'bedroom-nas', name: '卧室 NAS', host: '192.168.1.20', mac: '11:22:33:44:55:66', probePort: 443, offlineThreshold: 3 })],
			}),
		})))
		expect(screen.getByText('网络设备探测测试成功，已验证当前配置的可达性。')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			type: 'network',
			config: expect.objectContaining({ probeIntervalSeconds: 45, probeTimeoutSeconds: 5 }),
		}), false))
	})

	it('imports network device JSON into the visual device list', async () => {
		render(<ProviderForm provider={null} initialType="network" onCancel={() => {}} onSave={vi.fn()} />)
		await userEvent.click(screen.getByText('高级 JSON 导入 / 导出'))
		fireEvent.change(screen.getByLabelText('网络设备配置 JSON'), { target: { value: JSON.stringify({ probeIntervalSeconds: 60, devices: [{ id: 'office-pc', name: '书房电脑', host: '192.168.1.30', probePort: 3389, mac: 'AA:BB:CC:DD:EE:FF', wolPort: 7 }] }, null, 2) } })
		expect(screen.getByLabelText('网络设备 1 ID')).toHaveValue('office-pc')
		expect(screen.getByLabelText('网络设备 1 名称')).toHaveValue('书房电脑')
		expect(screen.getByLabelText('网络设备 1 Host')).toHaveValue('192.168.1.30')
		expect(screen.getByLabelText('网络设备 1 探测端口')).toHaveValue(3389)
		await userEvent.click(screen.getByText('单项高级覆盖（可选）'))
		expect(screen.getByLabelText('网络设备 1 WOL 端口覆盖')).toHaveValue(7)
	})

	it('exposes and saves a Tuya cloud Provider configuration', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const onTest = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="tuya" onCancel={() => {}} onSave={onSave} onTest={onTest} />)
		expect(screen.getByRole('option', { name: /Tuya 涂鸦云/ })).toBeInTheDocument()
		expect(screen.getByText('Tuya 登录方式')).toBeInTheDocument()
		await userEvent.selectOptions(screen.getByLabelText('Tuya 登录方式'), 'openapi')
		await userEvent.type(screen.getByPlaceholderText('tuya-main'), 'tuya-main')
		await userEvent.type(screen.getByLabelText('名称'), '涂鸦云')
		await userEvent.type(screen.getByLabelText('Tuya Access ID'), 'access-id')
		await userEvent.type(screen.getByLabelText('Tuya Access Secret'), 'access-secret')
		await userEvent.type(screen.getByLabelText('Tuya 用户 UID'), 'uid-123')
		await userEvent.click(screen.getByRole('button', { name: '测试 Tuya 连接' }))
		await waitFor(() => expect(onTest).toHaveBeenCalledWith(expect.objectContaining({ type: 'tuya', config: expect.objectContaining({ region: 'cn', accessId: 'access-id', accessSecret: 'access-secret', uid: 'uid-123' }) })))
		expect(screen.getByText('Tuya 云账号连接测试成功，设备目录可用。')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'tuya', config: expect.objectContaining({ accessId: 'access-id', accessSecret: 'access-secret', uid: 'uid-123', pollIntervalSeconds: 21600 }) }), false))
	})

	it('logs into eWeLink before saving a Sonoff Provider', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="sonoff" onCancel={() => {}} onSave={onSave} />)
		await userEvent.type(screen.getByPlaceholderText('sonoff-main'), 'sonoff-main')
		await userEvent.type(screen.getByLabelText('名称'), '易微联设备')
		await userEvent.type(screen.getByLabelText('eWeLink 账号'), 'user@example.com')
		await userEvent.type(screen.getByLabelText('eWeLink 密码'), 'password-1')
		await userEvent.click(screen.getByRole('button', { name: '登录 eWeLink 账号' }))
		await waitFor(() => expect(sonoffAPI.loginSonoff).toHaveBeenCalledWith(expect.objectContaining({ username: 'user@example.com', password: 'password-1', countryCode: '+86', region: 'auto' })))
		expect(screen.getByText(/eWeLink 登录成功/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ type: 'sonoff', config: expect.objectContaining({ mode: 'auto', region: 'cn', cloud: expect.objectContaining({ accessToken: 'sonoff-access', endpoint: 'https://cn-apia.coolkit.cn' }) }) }), false))
	})

	it('completes Tuya OAuth through the QR callback message and fills the UID/token', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const popup = {} as Window
		vi.spyOn(window, 'open').mockReturnValue(popup)
		render(<ProviderForm provider={null} initialType="tuya" onCancel={() => {}} onSave={onSave} />)
		await userEvent.selectOptions(screen.getByLabelText('Tuya 登录方式'), 'openapi')
		await userEvent.type(screen.getByPlaceholderText('tuya-main'), 'tuya-main')
		await userEvent.type(screen.getByLabelText('Tuya Access ID'), 'access-id')
		await userEvent.type(screen.getByLabelText('Tuya Access Secret'), 'access-secret')
		await userEvent.type(screen.getByLabelText('Tuya OAuth 授权页 URL'), 'https://auth.tuya.example/authorize')
		await userEvent.click(screen.getByRole('button', { name: '开始 Tuya OAuth 授权' }))
		await waitFor(() => expect(tuyaAPI.startTuyaOAuth).toHaveBeenCalledWith(expect.objectContaining({ accessId: 'access-id', accessSecret: 'access-secret', authorizationUrl: 'https://auth.tuya.example/authorize', redirectUrl: expect.stringContaining('/tuya/oauth/callback') })))
		expect(screen.getByRole('img', { name: 'Tuya OAuth 扫码授权二维码' })).toHaveAttribute('src', '/api/v1/tuya/oauth/qr?state=tuya-state')
		window.dispatchEvent(new MessageEvent('message', { origin: window.location.origin, data: { type: 'homeloom-tuya-oauth', code: 'auth-code', state: 'tuya-state' } }))
		await waitFor(() => expect(tuyaAPI.completeTuyaOAuth).toHaveBeenCalledWith({ state: 'tuya-state', code: 'auth-code' }))
		expect(await screen.findByText(/Tuya 扫码授权成功/)).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ uid: 'tuya-user', accessToken: 'tuya-access', refreshToken: 'tuya-refresh' }) }), false))
	})

	it('completes Home Assistant compatible Tuya QR login and fills sharing credentials', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		tuyaAPI.pollTuyaSharingLogin
			.mockResolvedValueOnce({ status: 'pending' })
			.mockResolvedValueOnce({ status: 'complete', accessToken: 'sharing-access', refreshToken: 'sharing-refresh', uid: 'sharing-user', endpoint: 'https://openapi.tuyaus.com', terminalId: 'terminal-1', expiresAt: '2030-01-01T01:00:00Z' })
		render(<ProviderForm provider={null} initialType="tuya" onCancel={() => {}} onSave={onSave} />)
		await userEvent.type(screen.getByPlaceholderText('tuya-main'), 'tuya-main')
		await userEvent.type(screen.getByLabelText('名称'), '涂鸦扫码云')
		await userEvent.type(screen.getByLabelText('Tuya User Code'), 'user-code-1')
		await userEvent.click(screen.getByRole('button', { name: '获取 Tuya 扫码二维码' }))
		await waitFor(() => expect(tuyaAPI.startTuyaSharingLogin).toHaveBeenCalledWith('user-code-1'))
		expect(screen.getByRole('img', { name: 'Tuya Home Assistant 扫码二维码' })).toHaveAttribute('src', '/api/v1/tuya/login/qr?state=sharing-state')
		await waitFor(() => expect(tuyaAPI.pollTuyaSharingLogin).toHaveBeenCalledWith('sharing-state'))
		tuyaAPI.pollTuyaSharingLogin.mockResolvedValue({ status: 'complete', accessToken: 'sharing-access', refreshToken: 'sharing-refresh', uid: 'sharing-user', endpoint: 'https://openapi.tuyaus.com', terminalId: 'terminal-1', expiresAt: '2030-01-01T01:00:00Z' })
		expect(await screen.findByText(/Tuya 扫码登录成功/, {}, { timeout: 3000 })).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ authType: 'sharing', uid: 'sharing-user', endpoint: 'https://openapi.tuyaus.com', terminalId: 'terminal-1', accessToken: 'sharing-access', refreshToken: 'sharing-refresh' }) }), false))
	})

	it('keeps the Gree device JSON as a backward-compatible advanced import', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="gree" onCancel={() => {}} onSave={onSave} />)
		const editor = screen.getByLabelText('Gree 设备配置 JSON')
		fireEvent.change(editor, { target: { value: JSON.stringify({ devices: [{ host: '192.168.1.42', port: 7000, mac: 'AA:BB:CC:DD:EE:FF', name: '客厅格力空调' }], pollIntervalSeconds: 60, requestTimeoutSeconds: 5 }, null, 2) } })
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))

		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			type: 'gree',
			config: { devices: [{ host: '192.168.1.42', port: 7000, mac: 'AA:BB:CC:DD:EE:FF', name: '客厅格力空调' }], pollIntervalSeconds: 60, requestTimeoutSeconds: 5 },
		}), false))
	})

	it('accepts v2 encryptionVersion from the advanced Gree JSON and normalizes it', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="gree" onCancel={() => {}} onSave={onSave} />)
		const editor = screen.getByLabelText('Gree 设备配置 JSON')
		fireEvent.change(editor, { target: { value: JSON.stringify({ devices: [{ host: '192.168.1.42', port: 7000, mac: 'AA:BB:CC:DD:EE:FF', name: '客厅格力空调', encryptionVersion: '2' }], pollIntervalSeconds: 60, requestTimeoutSeconds: 5 }, null, 2) } })

		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			type: 'gree',
			config: { devices: [{ host: '192.168.1.42', port: 7000, mac: 'AA:BB:CC:DD:EE:FF', name: '客厅格力空调', encryptionVersion: 2 }], pollIntervalSeconds: 60, requestTimeoutSeconds: 5 },
		}), false))
	})

	it('blocks unsupported encryptionVersion from the advanced Gree JSON before saving or testing', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		const onTest = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={null} initialType="gree" onCancel={() => {}} onSave={onSave} onTest={onTest} />)
		const editor = screen.getByLabelText('Gree 设备配置 JSON')
		fireEvent.change(editor, { target: { value: JSON.stringify({ devices: [{ host: '192.168.1.42', port: 7000, mac: 'AA:BB:CC:DD:EE:FF', name: '客厅格力空调', encryptionVersion: 3 }], pollIntervalSeconds: 60, requestTimeoutSeconds: 5 }, null, 2) } })

		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		expect(await screen.findAllByText(/Gree devices\[0\]\.encryptionVersion 配置无效/)).not.toHaveLength(0)
		expect(editor).toHaveAttribute('aria-invalid', 'true')
		expect(onSave).not.toHaveBeenCalled()

		expect(screen.queryByRole('button', { name: '测试 Gree 局域网连接' })).not.toBeInTheDocument()
		expect(onTest).not.toHaveBeenCalled()
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
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<ProviderForm provider={provider} onCancel={() => {}} onSave={onSave} />)
		await userEvent.click(screen.getByRole('button', { name: '发现小米中枢网关' }))
		await waitFor(() => expect(xiaomiAPI.discoverXiaomiGateways).toHaveBeenCalledOnce())
		const candidate = await screen.findByRole('button', { name: /central-hub.*192\.168\.1\.50/ })
		await userEvent.click(candidate)
		expect(screen.getByLabelText('小米中枢网关地址')).toHaveValue('192.168.1.50')
		expect(screen.getByLabelText('小米中枢网关端口')).toHaveValue(8883)
		await userEvent.click(screen.getByRole('button', { name: '保存并应用' }))
		await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ gatewayDid: 'hub-did' }) }), true))
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

		it('continues a Provider startup challenge through the Provider auth endpoint', async () => {
			const challenged: Provider = {
				id: 'xiaomi-cloud-main', type: 'xiaomi-miot-cloud', name: '小米云', enabled: true, status: 'auth_required', retryCount: 1,
				capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: false },
				config: { region: 'cn', username: 'owner@example.com', password: '********', devices: [] },
				authChallenge: { status: 'auth_required', challengeId: 'provider-challenge', verificationUrl: 'https://account.xiaomi.com/verify', expiresAt: '2030-01-01T00:00:00Z' },
			}
			xiaomiAPI.verifyXiaomiProviderAuthChallenge.mockResolvedValueOnce({ ...challenged, status: 'running', authChallenge: null, config: { ...challenged.config, serviceToken: '********' } })
			render(<ProviderForm provider={challenged} onCancel={() => {}} onSave={vi.fn()} />)
			expect(screen.getByRole('link', { name: '打开小米身份验证页面' })).toHaveAttribute('href', 'https://account.xiaomi.com/verify')
			await userEvent.type(screen.getByLabelText('小米 MIoT 云验证码'), '123456')
			await userEvent.click(screen.getByRole('button', { name: '提交验证码并继续 Provider' }))
			await waitFor(() => expect(xiaomiAPI.verifyXiaomiProviderAuthChallenge).toHaveBeenCalledWith('xiaomi-cloud-main', { challengeId: 'provider-challenge', code: '123456' }))
			expect(await screen.findByText(/小米短信验证成功，Provider 会话已更新/)).toBeInTheDocument()
		})

		it('shows an expired challenge state without allowing a stale code submission', async () => {
			xiaomiAPI.startXiaomiCloudLogin.mockResolvedValue({ status: 'verification_required', challengeId: 'expired-challenge', verificationUrl: 'https://account.xiaomi.com/fe/service/identity/authStart', expiresAt: '2020-01-01T00:00:00Z' })
			render(<ProviderForm provider={null} initialType="xiaomi-miot-cloud" onCancel={() => {}} onSave={vi.fn()} />)
			await userEvent.type(screen.getByLabelText('小米 MIoT 云账号'), 'owner@example.com')
			await userEvent.type(screen.getByLabelText('小米 MIoT 云密码'), 'account-password')
			await userEvent.click(screen.getByRole('button', { name: '登录小米云账号' }))
			expect(await screen.findByRole('alert')).toHaveTextContent('验证会话已过期')
			expect(screen.queryByLabelText('小米 MIoT 云验证码')).not.toBeInTheDocument()
			expect(xiaomiAPI.verifyXiaomiCloudLogin).not.toHaveBeenCalled()
		})

		it('clears a failed verification code and asks to restart an invalid challenge', async () => {
			xiaomiAPI.startXiaomiCloudLogin.mockResolvedValue({ status: 'verification_required', challengeId: 'challenge-invalid', verificationUrl: 'https://account.xiaomi.com/fe/service/identity/authStart', expiresAt: '2030-01-01T00:00:00Z' })
			xiaomiAPI.verifyXiaomiCloudLogin.mockRejectedValueOnce(new Error('Xiaomi identity verification challenge expired; start login again'))
			render(<ProviderForm provider={null} initialType="xiaomi-miot-cloud" onCancel={() => {}} onSave={vi.fn()} />)
			await userEvent.type(screen.getByLabelText('小米 MIoT 云账号'), 'owner@example.com')
			await userEvent.type(screen.getByLabelText('小米 MIoT 云密码'), 'account-password')
			await userEvent.click(screen.getByRole('button', { name: '登录小米云账号' }))
			const code = await screen.findByLabelText('小米 MIoT 云验证码')
			await userEvent.type(code, '123456')
			await userEvent.click(screen.getByRole('button', { name: '提交验证码并继续登录' }))
			expect(await screen.findByRole('alert')).toHaveTextContent('验证会话已过期或已失效')
			expect(screen.queryByLabelText('小米 MIoT 云验证码')).not.toBeInTheDocument()
			expect(screen.getByRole('button', { name: '登录小米云账号' })).toBeInTheDocument()
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
