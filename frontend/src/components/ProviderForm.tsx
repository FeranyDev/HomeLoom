import { useCallback, useEffect, useMemo, useState } from 'react'
import type { Provider, ProviderInput } from '../types/provider'
import { ApiError } from '../api/client'
import { completeTuyaOAuth, parseTuyaOAuthCallback, pollTuyaSharingLogin, startTuyaOAuth, startTuyaSharingLogin, tuyaOAuthQRCodeURL, tuyaSharingQRCodeURL, type TuyaOAuthCallbackMessage, type TuyaOAuthStartResult, type TuyaSharingLoginPollResult, type TuyaSharingLoginStartResult } from '../api/tuya'
import { completeXiaomiOAuth, discoverXiaomiGateways, getXiaomiProviderAuthChallenge, startXiaomiCloudLogin, startXiaomiOAuth, verifyXiaomiCloudLogin, verifyXiaomiProviderAuthChallenge, type XiaomiCloudLoginResult, type XiaomiGateway } from '../api/xiaomi'
import { loginSonoff } from '../api/sonoff'

const xiaomiOAuthRedirectURL = 'http://homeassistant.local:8123'
const tuyaOAuthDefaultRedirectURL = typeof window === 'undefined' ? 'http://homeassistant.local:8123/api/v1/tuya/oauth/callback' : `${window.location.origin}/api/v1/tuya/oauth/callback`
const expandedVirtualExamples = [
	['pressure', '室外气压', 'pressure-sensor'], ['noise', '客厅噪声', 'noise-sensor'],
	['water-level', '水箱水位', 'water-level-sensor'], ['soil-moisture', '花园土壤湿度', 'soil-moisture-sensor'],
	['illuminance', '客厅照度', 'illuminance-sensor'], ['occupancy', '书房占用', 'occupancy-sensor'],
	['leak', '厨房漏水', 'leak-sensor'], ['smoke', '客厅烟雾', 'smoke-sensor'],
	['carbon-monoxide', '一氧化碳监测', 'carbon-monoxide-sensor'], ['carbon-dioxide', '二氧化碳监测', 'carbon-dioxide-sensor'],
	['air-quality', '全屋空气质量', 'air-quality-sensor'], ['thermostat', '客厅恒温器', 'thermostat'],
	['air-conditioner', '客厅空调', 'air-conditioner'], ['heater-cooler', '卧室冷暖风机', 'heater-cooler'], ['humidifier', '书房加湿器', 'humidifier-dehumidifier'],
	['lock', '入户门锁', 'lock'], ['garage-door', '车库门', 'garage-door'],
	['security-system', '家庭安防', 'security-system'], ['valve', '花园水阀', 'valve'],
	['pump', '循环水泵', 'pump'], ['water-heater', '热水器', 'water-heater'],
	['power-meter', '全屋电力计量', 'power-meter'], ['ev-charger', '车库充电桩', 'ev-charger'],
	['speaker', '客厅扬声器', 'speaker'], ['television', '客厅电视', 'television'], ['robot-vacuum', '扫地机器人', 'robot-vacuum'],
].map(([id, name, type]) => ({ id: `demo-${id}`, name, type, online: true }))

function createXiaomiExample() {
	return { host: '', port: 8883, clientId: '', caCertificate: '', clientCertificate: '', privateKey: '', serverName: '', insecureSkipVerify: false, requestTimeoutSeconds: 10, pollIntervalSeconds: 60, oauth: { clientId: '', region: 'cn', redirectUrl: xiaomiOAuthRedirectURL, oauthUuid: '', virtualDid: '' }, devices: [] }
}

function createXiaomiMIoTCloudExample() {
	return { region: 'cn', username: '', password: '', pollIntervalSeconds: 30, requestTimeoutSeconds: 15, devices: [] }
}

function createGreeExample() {
	return { devices: [], pollIntervalSeconds: 60, requestTimeoutSeconds: 5 }
}

function createNetworkExample() {
	return {
		probeMethod: 'tcp',
		probeIntervalSeconds: 30,
		probeTimeoutSeconds: 3,
		wakeGraceSeconds: 300,
		onlineThreshold: 1,
		offlineThreshold: 2,
		wolBroadcastAddress: '255.255.255.255',
		wolPort: 9,
		devices: [],
	}
}

function createTuyaExample() {
	return { authType: 'sharing', region: 'cn', userCode: '', uid: '', endpoint: '', terminalId: '', accessToken: '', refreshToken: '', requestTimeoutSeconds: 15, pollIntervalSeconds: 60, mqtt: { enabled: false }, quirks: [] }
}

function createSonoffExample() {
	return { mode: 'auto', region: 'auto', requestTimeoutSeconds: 10, refreshIntervalSeconds: 60, discoveryTimeoutSeconds: 5, cloud: { endpoint: '', accessToken: '', username: '', password: '', countryCode: '+86', appId: '', appSecret: '', websocketEndpoint: '' }, devices: [] }
}

function createCameraExample() {
	return { cameras: [] }
}

function createVirtualExample() {
	return { latencyMs: 0, rejectWrites: false, devices: [] }
}

function createMQTTExample(mode: 'client' | 'server' = 'client') {
	if (mode === 'server') return { mode, listenAddress: '127.0.0.1:1883', username: '', password: '', connectTimeoutSeconds: 10, retainedStateMaxAgeSeconds: 300, tls: {}, devices: [] }
	return { mode, brokerUrl: 'mqtt://127.0.0.1:1883', username: '', password: '', clientId: '', keepAliveSeconds: 30, connectTimeoutSeconds: 10, sessionExpirySeconds: 86400, retainedStateMaxAgeSeconds: 300, tls: {}, devices: [] }
}

type ProviderSelection = 'virtual' | 'mqtt-client' | 'mqtt-server' | 'xiaomi' | 'xiaomi-miot-cloud' | 'gree' | 'network' | 'tuya' | 'sonoff' | 'camera'

function objectRecord(value: unknown): Record<string, unknown> {
	return value && !Array.isArray(value) && typeof value === 'object' ? value as Record<string, unknown> : {}
}

function firstGreeDevice(config: Record<string, unknown>): Record<string, unknown> {
	const devices = Array.isArray(config.devices) ? config.devices : []
	const first = objectRecord(devices[0])
	if (Object.keys(first).length > 0) return first
	return {
		host: config.host ?? '',
		port: config.port ?? 7000,
		mac: config.mac ?? '',
		name: config.name ?? '',
		...(config.encryptionKey !== undefined ? { encryptionKey: config.encryptionKey } : {}),
		...(config.encryptionVersion !== undefined ? { encryptionVersion: config.encryptionVersion } : {}),
	}
}

function normalizeGreeConfig(config: Record<string, unknown>): string | null {
	const hasLegacyDeviceFields = ['host', 'port', 'mac', 'name', 'encryptionKey', 'encryptionVersion'].some((field) => config[field] !== undefined && config[field] !== '')
	const devices = Array.isArray(config.devices) ? config.devices : hasLegacyDeviceFields ? [firstGreeDevice(config)] : []
	const first = objectRecord(devices[0])
	if (config.pollIntervalSeconds === undefined && first.pollIntervalSeconds !== undefined) config.pollIntervalSeconds = first.pollIntervalSeconds
	if (config.requestTimeoutSeconds === undefined && first.requestTimeoutSeconds !== undefined) config.requestTimeoutSeconds = first.requestTimeoutSeconds
	for (let index = 0; index < devices.length; index += 1) {
		const encryptionVersion = objectRecord(devices[index]).encryptionVersion
		if (encryptionVersion !== undefined && encryptionVersion !== null && encryptionVersion !== '' && encryptionVersion !== 1 && encryptionVersion !== 2 && encryptionVersion !== '1' && encryptionVersion !== '2') {
			const displayedValue = typeof encryptionVersion === 'string' ? JSON.stringify(encryptionVersion) : String(encryptionVersion)
			return `Gree devices[${index}].encryptionVersion 配置无效：仅支持未设置、空字符串或 v1/v2（数字 1/2 / 字符串 "1"/"2"），当前值为 ${displayedValue}`
		}
	}
	config.devices = devices.map((item) => {
		const device = { ...objectRecord(item) }
		for (const field of ['pollIntervalSeconds', 'requestTimeoutSeconds']) {
			delete device[field]
		}
		if (device.encryptionVersion === '1') device.encryptionVersion = 1
		else if (device.encryptionVersion === '2') device.encryptionVersion = 2
		else if (device.encryptionVersion === undefined || device.encryptionVersion === null || device.encryptionVersion === '') delete device.encryptionVersion
		return device
	})
	delete config.encryptionVersion
	for (const field of ['pollIntervalSeconds', 'requestTimeoutSeconds']) {
		if (config[field] === '') delete config[field]
	}
	return null
}

function challengeExpiryTimestamp(expiresAt?: string): number | null {
	if (!expiresAt) return null
	const timestamp = Date.parse(expiresAt)
	return Number.isFinite(timestamp) ? timestamp : null
}

function normalizeCloudChallenge(value: unknown): XiaomiCloudLoginResult | null {
	const source = objectRecord(value)
	const nested = objectRecord(source.challenge ?? source.authChallenge ?? source.auth_challenge)
	const raw = Object.keys(nested).length > 0 ? nested : source
	const challengeId = String(raw.challengeId ?? raw.challenge_id ?? raw.id ?? '').trim()
	if (!challengeId) return null
	const verificationUrl = String(raw.verificationUrl ?? raw.verification_url ?? raw.url ?? '').trim()
	const expiresAt = String(raw.expiresAt ?? raw.expires_at ?? '').trim()
	const status = String(raw.status ?? 'auth_required').trim() || 'auth_required'
	const message = String(raw.message ?? raw.description ?? '').trim()
	return { status, challengeId, ...(verificationUrl ? { verificationUrl } : {}), ...(expiresAt ? { expiresAt } : {}), ...(message ? { message } : {}) }
}

function isCloudChallengeStatus(status: unknown): boolean {
	return ['verification_required', 'auth_required', 'authentication_required', 'challenge', 'pending_verification'].includes(String(status ?? '').trim().toLowerCase())
}

function isExpiredCloudChallengeError(cause: unknown): boolean {
	const message = cause instanceof Error ? cause.message : String(cause ?? '')
	return /challenge.*(?:missing|expired)|expired.*challenge|start login again|too many .*attempts/i.test(message)
}

export function ProviderForm({ provider, initialType, onCancel, onSave, onTest }: { provider: Provider | null; initialType?: ProviderSelection | 'mqtt'; onCancel: () => void; onSave: (input: ProviderInput, editing: boolean) => Promise<void>; onTest?: (input: ProviderInput) => Promise<void> }) {
	const selectedInitialType: ProviderSelection = provider?.type === 'mqtt' ? (String(provider.config.mode ?? 'client') === 'server' ? 'mqtt-server' : 'mqtt-client') : initialType === 'mqtt' ? 'mqtt-client' : (provider?.type ?? initialType ?? 'virtual') as ProviderSelection
	const initialXiaomiConfig = createXiaomiExample()
	const initialXiaomiCloudConfig = createXiaomiMIoTCloudExample()
	const initialGreeConfig = createGreeExample()
	const initialNetworkConfig = createNetworkExample()
	const initialTuyaConfig = createTuyaExample()
	const initialSonoffConfig = createSonoffExample()
	const initialCameraConfig = createCameraExample()
	const initialMQTTConfig = createMQTTExample(selectedInitialType === 'mqtt-server' ? 'server' : 'client')
  const [id, setID] = useState(provider?.id ?? '')
  const [name, setName] = useState(provider?.name ?? '')
  const [type, setType] = useState(selectedInitialType)
  const [enabled, setEnabled] = useState(provider?.enabled ?? true)
	  const [config, setConfig] = useState(JSON.stringify(provider?.config ?? (selectedInitialType === 'mqtt-client' || selectedInitialType === 'mqtt-server' ? initialMQTTConfig : selectedInitialType === 'xiaomi' ? initialXiaomiConfig : selectedInitialType === 'xiaomi-miot-cloud' ? initialXiaomiCloudConfig : selectedInitialType === 'gree' ? initialGreeConfig : selectedInitialType === 'network' ? initialNetworkConfig : selectedInitialType === 'tuya' ? initialTuyaConfig : selectedInitialType === 'sonoff' ? initialSonoffConfig : selectedInitialType === 'camera' ? initialCameraConfig : createVirtualExample()), null, 2))
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
	const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
	const [authorizing, setAuthorizing] = useState(false)
	const [completingOAuth, setCompletingOAuth] = useState(false)
	const [oauthCallbackURL, setOAuthCallbackURL] = useState('')
	const [gateways, setGateways] = useState<XiaomiGateway[]>([])
	const [discoveringGateways, setDiscoveringGateways] = useState(false)
	const initialProviderCloudChallenge = normalizeCloudChallenge(provider?.authChallenge)
	const [cloudChallenge, setCloudChallenge] = useState<XiaomiCloudLoginResult | null>(initialProviderCloudChallenge)
	const [cloudChallengeSource, setCloudChallengeSource] = useState<'provider' | 'login' | null>(initialProviderCloudChallenge ? 'provider' : null)
	const [cloudVerificationCode, setCloudVerificationCode] = useState('')
	const [cloudAuthenticating, setCloudAuthenticating] = useState(false)
	const [cloudChallengeClock, setCloudChallengeClock] = useState(() => Date.now())
	const [providerChallengeUnavailable, setProviderChallengeUnavailable] = useState(false)
	const [tuyaOAuthSession, setTuyaOAuthSession] = useState<TuyaOAuthStartResult | null>(null)
	const [tuyaOAuthCallbackURL, setTuyaOAuthCallbackURL] = useState('')
	const [tuyaOAuthBusy, setTuyaOAuthBusy] = useState(false)
	const [tuyaSharingSession, setTuyaSharingSession] = useState<TuyaSharingLoginStartResult | null>(null)
	const [tuyaSharingBusy, setTuyaSharingBusy] = useState(false)
	const [sonoffAuthenticating, setSonoffAuthenticating] = useState(false)
	const hasRedactedSecrets = Boolean(provider && JSON.stringify(provider.config).includes('********'))
  const example = { latencyMs: 0, rejectWrites: false, devices: [{ id: 'demo-switch', name: '客厅开关', type: 'switch', online: true, power: false }, { id: 'demo-light', name: '客厅灯', type: 'lightbulb', online: true, power: true, brightness: 80, colorTemperature: 250, hue: 35, saturation: 45 }, { id: 'demo-outlet', name: '书房插座', type: 'outlet', online: true, power: false, inUse: false, currentPower: 0, energy: 1.25 }, { id: 'demo-temperature', name: '客厅温度', type: 'temperature-sensor', online: true, temperature: 23.6, batteryLevel: 91, lowBattery: false }, { id: 'demo-humidity', name: '客厅湿度', type: 'humidity-sensor', online: true, humidity: 56.2, batteryLevel: 90, lowBattery: false }, { id: 'demo-climate', name: '客厅温湿度', type: 'temperature-humidity-sensor', online: true, temperature: 23.6, humidity: 56.2, batteryLevel: 87, lowBattery: false }, { id: 'demo-contact', name: '入户门', type: 'contact-sensor', online: true, contact: false, batteryLevel: 88, lowBattery: false, tampered: false }, { id: 'demo-motion', name: '走廊活动', type: 'motion-sensor', online: true, motion: false, batteryLevel: 84, lowBattery: false, tampered: false }, { id: 'demo-fan', name: '卧室风扇', type: 'fan', online: true, active: false, speed: 35, mode: 'manual', swingMode: true, direction: 'clockwise', controlLock: false }, { id: 'demo-air', name: '客厅净化器', type: 'air-purifier', online: true, active: true, speed: 60, mode: 'auto', swingMode: false, controlLock: false, airQuality: 'good', pm25: 12, voc: 80, filterLife: 82, filterChange: false }, { id: 'demo-shade', name: '南窗帘', type: 'window-covering', online: true, position: 50, obstruction: false }, ...expandedVirtualExamples] }
	const xiaomiExample = initialXiaomiConfig
	const xiaomiCloudExample = initialXiaomiCloudConfig
	const greeExample = initialGreeConfig
	const networkExample = initialNetworkConfig
	const configObject = useMemo(() => {
		try {
			const parsed = JSON.parse(config) as unknown
			if (parsed && !Array.isArray(parsed) && typeof parsed === 'object') return parsed as Record<string, unknown>
		} catch { /* validation is shown on submit */ }
		return {}
	}, [config])
	const tlsConfig = configObject.tls && !Array.isArray(configObject.tls) && typeof configObject.tls === 'object' ? configObject.tls as Record<string, unknown> : {}
	const xiaomiOAuth = configObject.oauth && !Array.isArray(configObject.oauth) && typeof configObject.oauth === 'object' ? configObject.oauth as Record<string, unknown> : {}
	const mqttSelected = type === 'mqtt-client' || type === 'mqtt-server'
	const mqttServer = type === 'mqtt-server'
	const providerType = mqttSelected ? 'mqtt' : type
	const updateMQTT = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, [key]: value }, null, 2))
	const updateTLS = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, tls: { ...tlsConfig, [key]: value } }, null, 2))
	const updateXiaomi = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, [key]: value }, null, 2))
	const updateGreeRuntime = (key: string, value: unknown) => {
		const nextConfig: Record<string, unknown> = { ...configObject }
		if (value === undefined) delete nextConfig[key]
		else nextConfig[key] = value
		setConfig(JSON.stringify(nextConfig, null, 2))
	}
	const updateNetworkRuntime = (key: string, value: unknown) => {
		const nextConfig: Record<string, unknown> = { ...configObject }
		if (value === undefined) delete nextConfig[key]
		else nextConfig[key] = value
		setConfig(JSON.stringify(nextConfig, null, 2))
	}
	const updateTuya = (key: string, value: unknown) => {
		const nextConfig: Record<string, unknown> = { ...configObject }
		if (value === undefined) delete nextConfig[key]
		else nextConfig[key] = value
		setConfig(JSON.stringify(nextConfig, null, 2))
	}
	const sonoffCloud = configObject.cloud && !Array.isArray(configObject.cloud) && typeof configObject.cloud === 'object' ? configObject.cloud as Record<string, unknown> : {}
	const updateSonoffCloud = (key: string, value: unknown) => {
		const nextCloud: Record<string, unknown> = { ...sonoffCloud }
		if (value === undefined) delete nextCloud[key]
		else nextCloud[key] = value
		setConfig(JSON.stringify({ ...configObject, cloud: nextCloud }, null, 2))
	}
	const updateXiaomiOAuth = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, oauth: { ...xiaomiOAuth, [key]: value } }, null, 2))
	const updateXiaomiCloudIdentity = (key: string, value: unknown) => { updateXiaomi(key, value); setCloudChallenge(null); setCloudChallengeSource(null); setCloudVerificationCode('') }
	const cloudSessionReady = ['userId', 'ssecurity', 'serviceToken'].every((key) => typeof configObject[key] === 'string' && String(configObject[key]).length > 0)
	const cloudMISSConfigured = Array.isArray(configObject.devices) && configObject.devices.some((item) => {
		if (!item || Array.isArray(item) || typeof item !== 'object') return false
		const media = (item as Record<string, unknown>).media
		return Boolean(media && !Array.isArray(media) && typeof media === 'object' && (media as Record<string, unknown>).protocol === 'xiaomi-miss')
	})
	const cloudMediaSessionReady = !cloudMISSConfigured || (typeof configObject.passToken === 'string' && configObject.passToken.length > 0)
	const cloudChallengeExpiresAt = challengeExpiryTimestamp(cloudChallenge?.expiresAt)
	const cloudChallengeExpired = cloudChallengeExpiresAt !== null && cloudChallengeExpiresAt <= cloudChallengeClock
	const providerNeedsCloudChallenge = Boolean(provider && provider.type === 'xiaomi-miot-cloud' && isCloudChallengeStatus(provider.status) && !cloudChallenge)
	const providerID = provider?.id
	const tuyaAuthType = String(configObject.authType ?? 'openapi').trim().toLowerCase() || 'openapi'
	async function beginTuyaSharingLogin() {
		const userCode = String(configObject.userCode ?? '').trim()
		if (!userCode || userCode === '********') { setError('请重新填写 Tuya/Smart Life App 中的 User Code'); return }
		setTuyaSharingBusy(true); setError(null); setTestResult(null)
		try {
			const result = await startTuyaSharingLogin(userCode)
			setTuyaSharingSession(result)
			setTestResult('Tuya 扫码二维码已生成，请使用 Tuya/Smart Life App 扫描并确认。')
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '无法生成 Tuya 扫码二维码')
		} finally { setTuyaSharingBusy(false) }
	}
	async function beginSonoffLogin() {
		const username = String(sonoffCloud.username ?? '').trim()
		const password = String(sonoffCloud.password ?? '').trim()
		if (!username || !password || password === '********') { setError('请填写 eWeLink 邮箱/手机号和密码后再登录'); return }
		setSonoffAuthenticating(true); setError(null); setTestResult(null)
		try {
			const result = await loginSonoff({
				username, password, countryCode: String(sonoffCloud.countryCode ?? '+86'),
				region: String(configObject.region ?? 'auto'), endpoint: String(sonoffCloud.endpoint ?? ''),
				appId: String(sonoffCloud.appId ?? ''), appSecret: String(sonoffCloud.appSecret ?? ''),
			})
			setConfig(JSON.stringify({ ...configObject, region: result.region, cloud: { ...sonoffCloud, accessToken: result.accessToken, endpoint: result.endpoint } }, null, 2))
			setTestResult('eWeLink 登录成功，云端设备目录和局域网 devicekey 已就绪。请保存 Provider。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : 'eWeLink 登录失败') } finally { setSonoffAuthenticating(false) }
	}
	useEffect(() => {
		if (!tuyaSharingSession) return
		let active = true
		const poll = async () => {
			try {
				const result: TuyaSharingLoginPollResult = await pollTuyaSharingLogin(tuyaSharingSession.state)
				if (!active) return
				if (result.status === 'pending') return
				if (result.status === 'expired') {
					setTuyaSharingSession(null)
					setError(result.message || 'Tuya 扫码会话已过期，请重新生成二维码')
					return
				}
				if (result.status !== 'complete' || !result.accessToken || !result.refreshToken || !result.uid || !result.endpoint || !result.terminalId) {
					setTuyaSharingSession(null)
					setError(result.message || 'Tuya 扫码登录返回信息不完整，请重试')
					return
				}
				setConfig(current => {
					let parsed: Record<string, unknown> = {}
					try {
						const value = JSON.parse(current) as unknown
						if (value && !Array.isArray(value) && typeof value === 'object') parsed = value as Record<string, unknown>
					} catch { /* the editor will show validation on save */ }
					return JSON.stringify({ ...parsed, authType: 'sharing', endpoint: result.endpoint, clientId: 'HA_3y9q4ak7g4ephrvke', terminalId: result.terminalId, accessToken: result.accessToken, refreshToken: result.refreshToken, uid: result.uid, tokenExpiresAt: result.expiresAt }, null, 2)
				})
				setTuyaSharingSession(null)
				setTestResult('Tuya 扫码登录成功，UID 与会话 Token 已就绪；现在可以保存并启用 Provider。')
			} catch (cause) {
				if (!active) return
				setTuyaSharingSession(null)
				setError(cause instanceof Error ? cause.message : 'Tuya 扫码登录失败')
			}
		}
		const timer = window.setInterval(() => void poll(), 2000)
		void poll()
		return () => { active = false; window.clearInterval(timer) }
	}, [tuyaSharingSession])
	const tuyaCallbackFromInput = useCallback((input: string): TuyaOAuthCallbackMessage | null => {
		const value = input.trim()
		if (!value) return null
		try {
			const callbackURL = new URL(value)
			return parseTuyaOAuthCallback({ type: 'homeloom-tuya-oauth', code: callbackURL.searchParams.get('code') ?? '', state: callbackURL.searchParams.get('state') ?? '', error: callbackURL.searchParams.get('error') ?? '' })
		} catch {
			return parseTuyaOAuthCallback({ type: 'homeloom-tuya-oauth', code: value, state: tuyaOAuthSession?.state ?? '', error: '' })
		}
	}, [tuyaOAuthSession])
	const completeTuyaAuthorization = useCallback(async (callback?: TuyaOAuthCallbackMessage) => {
		const supplied = callback ?? tuyaCallbackFromInput(tuyaOAuthCallbackURL)
		const state = String(supplied?.state ?? tuyaOAuthSession?.state ?? '').trim()
		const code = String(supplied?.code ?? '').trim()
		if (supplied?.error) { setError(`Tuya 授权失败：${supplied.error}`); return }
		if (!state || !code) { setError('请先完成 Tuya 扫码授权，并粘贴回调 URL'); return }
		if (!tuyaOAuthSession || state !== tuyaOAuthSession.state) { setError('Tuya OAuth state 不匹配，请从当前窗口重新开始扫码授权'); return }
		setTuyaOAuthBusy(true); setError(null); setTestResult(null)
		try {
			const result = await completeTuyaOAuth({ state, code })
			setConfig(JSON.stringify({ ...configObject, accessToken: result.accessToken, refreshToken: result.refreshToken, uid: result.uid, tokenExpiresAt: result.expiresAt, redirectUrl: String(configObject.redirectUrl ?? tuyaOAuthDefaultRedirectURL) }, null, 2))
			setTuyaOAuthSession(null); setTuyaOAuthCallbackURL('')
			setTestResult('Tuya 扫码授权成功，账号 Token 与 UID 已就绪；现在可以保存并启用 Provider。')
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : 'Tuya OAuth 授权失败')
		} finally { setTuyaOAuthBusy(false) }
	}, [configObject, tuyaOAuthCallbackURL, tuyaOAuthSession, tuyaCallbackFromInput])
	async function beginTuyaOAuth() {
		const accessId = String(configObject.accessId ?? '').trim()
		const accessSecret = String(configObject.accessSecret ?? '').trim()
		const authorizationUrl = String(configObject.authorizationUrl ?? '').trim()
		if (!accessId || !accessSecret || accessSecret === '********') { setError('开始 Tuya 扫码前，请填写真实的 Access ID 和 Access Secret'); return }
		if (!authorizationUrl) { setError('请先填写 Tuya IoT Platform 生成的 OAuth H5 授权页 URL'); return }
		setTuyaOAuthBusy(true); setError(null); setTestResult(null)
		try {
			const result = await startTuyaOAuth({ accessId, accessSecret, region: String(configObject.region ?? 'cn'), baseUrl: String(configObject.baseUrl ?? '') || undefined, authorizationUrl, redirectUrl: String(configObject.redirectUrl ?? tuyaOAuthDefaultRedirectURL) })
			setTuyaOAuthSession(result)
			const popup = typeof window !== 'undefined' ? window.open(result.authorizationUrl, 'homeloom-tuya-oauth', 'popup,width=720,height=760') : null
			setTestResult(popup ? 'Tuya 授权页已打开；也可以使用下方二维码让 Tuya/Smart Life App 扫码。' : 'Tuya 授权已准备好，请使用下方二维码让 Tuya/Smart Life App 扫码。')
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '无法开始 Tuya OAuth 授权')
		} finally { setTuyaOAuthBusy(false) }
	}
	useEffect(() => {
		const listener = (event: MessageEvent) => {
			if (typeof window === 'undefined' || event.origin !== window.location.origin) return
			const callback = parseTuyaOAuthCallback(event.data)
			if (callback) void completeTuyaAuthorization(callback)
		}
		window.addEventListener('message', listener)
		return () => window.removeEventListener('message', listener)
	}, [completeTuyaAuthorization])
	useEffect(() => {
		if (!providerNeedsCloudChallenge || !providerID) return
		let active = true
		setProviderChallengeUnavailable(false)
		void getXiaomiProviderAuthChallenge(providerID).then((challenge) => {
			if (!active) return
			const normalized = normalizeCloudChallenge(challenge)
			if (normalized) {
				setCloudChallenge(normalized)
				setCloudChallengeSource('provider')
				setProviderChallengeUnavailable(false)
				setTestResult('Provider 启动需要小米短信验证。请打开验证页发送验证码，再回到这里提交。')
			} else setProviderChallengeUnavailable(true)
		}).catch((cause) => {
			if (!active) return
			setProviderChallengeUnavailable(true)
			setError(cause instanceof Error ? cause.message : '读取小米验证会话失败')
		})
		return () => { active = false }
	}, [providerNeedsCloudChallenge, providerID])
	useEffect(() => {
		if (cloudChallengeExpiresAt === null) return
		const delay = cloudChallengeExpiresAt - Date.now()
		if (delay <= 0) {
			setCloudChallengeClock(Date.now())
			return
		}
		// Browsers clamp setTimeout to a signed 32-bit duration. Challenges are
		// normally ten minutes long, but keep fixtures or a clock-skewed server
		// response from overflowing and firing immediately.
		const timer = window.setTimeout(() => setCloudChallengeClock(Date.now()), Math.min(delay + 1, 2_147_483_647))
		return () => window.clearTimeout(timer)
	}, [cloudChallengeExpiresAt])
	function applyCloudSession(result: XiaomiCloudLoginResult) {
		if (result.status !== 'verified' || !result.userId || !result.ssecurity || !result.serviceToken) throw new Error('小米云登录未返回完整会话，请重新登录')
		setConfig(JSON.stringify({
			...configObject,
			userId: result.userId,
			ssecurity: result.ssecurity,
			serviceToken: result.serviceToken,
			...(result.passToken ? { passToken: result.passToken } : {}),
		}, null, 2))
		setCloudChallenge(null); setCloudChallengeSource(null); setCloudVerificationCode('')
		setTestResult('小米云账号验证成功，会话凭据已就绪；现在可以保存并启用 Provider。')
	}
	async function beginCloudLogin() {
		const username = String(configObject.username ?? '').trim()
		const password = String(configObject.password ?? '')
		if (!username || !password || password === '********') { setError('请输入当前的小米账号和真实密码后再登录'); return }
		setCloudAuthenticating(true); setError(null); setTestResult(null); setCloudChallenge(null); setCloudChallengeSource(null); setCloudVerificationCode(''); setProviderChallengeUnavailable(false)
		try {
			const result = await startXiaomiCloudLogin({ region: String(configObject.region ?? 'cn'), username, password, requestTimeoutSeconds: Number(configObject.requestTimeoutSeconds ?? 15) })
			const challenge = normalizeCloudChallenge(result)
			if (challenge && isCloudChallengeStatus(result.status)) {
				if (!challenge.verificationUrl) throw new Error('小米要求身份验证，但没有返回验证入口')
				setCloudChallenge(challenge); setCloudChallengeSource('login'); setProviderChallengeUnavailable(false)
				setTestResult('小米要求身份验证。请打开验证页面发送短信或邮件验证码，然后回到这里填写。')
			} else applyCloudSession(result)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '小米云登录失败') } finally { setCloudAuthenticating(false) }
	}
	async function completeCloudVerification() {
		if (!cloudChallenge?.challengeId || !cloudVerificationCode.trim()) return
		if (cloudChallengeExpired) {
			setCloudChallenge(null)
			setCloudChallengeSource(null)
			setCloudVerificationCode('')
			setError('小米验证会话已过期，请重新登录后再次获取验证码')
			return
		}
		setCloudAuthenticating(true); setError(null)
		try {
			if (cloudChallengeSource === 'provider' && provider?.id) {
				const updated = await verifyXiaomiProviderAuthChallenge(provider.id, { challengeId: cloudChallenge.challengeId, code: cloudVerificationCode.trim() })
				const returnedConfig = objectRecord(updated.config)
				// The provider endpoint persists the session server-side and redacts
				// secrets in its response. Merge only non-redacted values so the
				// editor never replaces an existing secret with a placeholder.
				const nextConfig = { ...configObject }
				for (const key of ['userId', 'ssecurity', 'serviceToken', 'passToken']) {
					const value = returnedConfig[key]
					if (typeof value === 'string' && value && value !== '********') nextConfig[key] = value
					else if (value === '********' && !(key in nextConfig)) nextConfig[key] = value
				}
				setConfig(JSON.stringify(nextConfig, null, 2))
				setCloudChallenge(null); setCloudChallengeSource(null); setCloudVerificationCode('')
				setTestResult('小米短信验证成功，Provider 会话已更新；现在可以关闭此窗口。')
			} else applyCloudSession(await verifyXiaomiCloudLogin({ challengeId: cloudChallenge.challengeId, code: cloudVerificationCode.trim() }))
		}
		catch (cause) {
			setCloudVerificationCode('')
			if (isExpiredCloudChallengeError(cause)) {
				setCloudChallenge(null)
				setCloudChallengeSource(null)
				setError('小米验证会话已过期或已失效，请重新登录后再次获取验证码')
			} else setError(cause instanceof Error ? cause.message : '小米验证码校验失败')
		}
		finally { setCloudAuthenticating(false) }
	}
	async function authorizeXiaomi() {
		setAuthorizing(true); setError(null); setTestResult(null)
		const popup = window.open('', 'homeloom-xiaomi-oauth', 'popup,width=720,height=760')
		try {
			if (!popup) throw new Error('浏览器阻止了授权窗口，请允许此站点打开弹窗')
			const input = { clientId: String(xiaomiOAuth.clientId ?? ''), region: String(xiaomiOAuth.region ?? 'cn'), redirectUrl: xiaomiOAuthRedirectURL, oauthUuid: String(xiaomiOAuth.oauthUuid ?? '') || undefined, virtualDid: String(xiaomiOAuth.virtualDid ?? '') || undefined }
			const started = await startXiaomiOAuth(input)
			const nextOAuth = { ...xiaomiOAuth, ...input, oauthUuid: started.oauthUuid, virtualDid: started.virtualDid }
			setConfig(JSON.stringify({ ...configObject, oauth: nextOAuth, clientId: started.virtualDid }, null, 2))
			popup.location.href = started.authorizationUrl
			setTestResult('授权页面已打开。完成授权后，请复制浏览器地址栏中的完整 URL 并粘贴到下方。')
		} catch (cause) { popup?.close(); setError(cause instanceof Error ? cause.message : '小米授权失败') } finally { setAuthorizing(false) }
	}
	async function completeXiaomiAuthorization() {
		setCompletingOAuth(true); setError(null); setTestResult(null)
		try {
			const callback = new URL(oauthCallbackURL.trim())
			if (callback.origin !== xiaomiOAuthRedirectURL) throw new Error(`回调 URL 必须以 ${xiaomiOAuthRedirectURL} 开头`)
			const oauthError = callback.searchParams.get('error')
			if (oauthError) throw new Error(callback.searchParams.get('error_description') ?? `小米授权失败：${oauthError}`)
			const code = callback.searchParams.get('code') ?? ''
			const state = callback.searchParams.get('state') ?? ''
			if (!code || !state) throw new Error('回调 URL 中缺少 code 或 state，请复制浏览器地址栏中的完整 URL')
			const input = { clientId: String(xiaomiOAuth.clientId ?? ''), region: String(xiaomiOAuth.region ?? 'cn'), redirectUrl: xiaomiOAuthRedirectURL, oauthUuid: String(xiaomiOAuth.oauthUuid ?? '') || undefined, virtualDid: String(xiaomiOAuth.virtualDid ?? '') || undefined }
			const provisioned = await completeXiaomiOAuth({ ...input, code, state })
			setConfig(JSON.stringify({ ...configObject, ...provisioned, oauth: provisioned.oauth }, null, 2))
			setOAuthCallbackURL('')
			setTestResult('OAuth 与中枢客户端证书已就绪。下一步请选择中枢并测试 MQTT 连接。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : '无法解析小米授权回调 URL') } finally { setCompletingOAuth(false) }
	}
	async function discoverGateways() {
		setDiscoveringGateways(true); setError(null)
		try { setGateways(await discoverXiaomiGateways()) } catch (cause) { setError(cause instanceof Error ? cause.message : '中枢发现失败') } finally { setDiscoveringGateways(false) }
	}
	  async function submit(event: React.FormEvent) {
	    event.preventDefault(); let parsed: Record<string, unknown>
		try { parsed = JSON.parse(config) as Record<string, unknown>; if (!parsed || Array.isArray(parsed)) throw new Error() } catch { setError('扩展配置必须是 JSON 对象'); setFieldErrors({ config: '必须是 JSON 对象' }); return }
		if (mqttSelected) parsed.mode = mqttServer ? 'server' : 'client'
		if ((mqttSelected || type === 'xiaomi' || type === 'xiaomi-miot-cloud') && !Array.isArray(parsed.devices)) parsed.devices = []
		if (type === 'gree') {
			const greeConfigError = normalizeGreeConfig(parsed)
			if (greeConfigError) { setError(greeConfigError); setFieldErrors({ config: greeConfigError }); return }
		}
		if (type === 'virtual' && !provider && !Array.isArray(parsed.devices)) parsed.devices = []
		if (type === 'camera' && !Array.isArray(parsed.cameras)) parsed.cameras = []
		if (type === 'sonoff') {
			const mode = String(parsed.mode ?? 'auto').toLowerCase()
			const cloud = objectRecord(parsed.cloud)
			if (mode !== 'local' && !String(cloud.accessToken ?? '').trim() && (!String(cloud.username ?? '').trim() || !String(cloud.password ?? '').trim())) { setError('Sonoff auto/cloud 模式请先完成 eWeLink 账号登录，或填写已有 Access Token'); return }
		}
		if (type === 'xiaomi-miot-cloud' && !['userId', 'ssecurity', 'serviceToken'].every((key) => typeof parsed[key] === 'string' && String(parsed[key]).length > 0)) { setError('请先完成“小米云账号登录”；如触发短信或邮件验证，请回填验证码后再保存'); return }
		if (type === 'xiaomi-miot-cloud' && cloudMISSConfigured && (typeof parsed.passToken !== 'string' || parsed.passToken.length === 0)) { setError('已配置小米摄像头，请使用账号密码重新登录以取得摄像头所需的 passToken'); return }
		setSaving(true); setError(null); setFieldErrors({}); try { await onSave({ id, name, type: providerType, enabled, config: parsed }, Boolean(provider)) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败'); if (cause instanceof ApiError) setFieldErrors(cause.fields) } finally { setSaving(false) }
  }
	async function testConnection() {
		let parsed: Record<string, unknown>
		try { parsed = JSON.parse(config) as Record<string, unknown>; if (!parsed || Array.isArray(parsed)) throw new Error() } catch { setError('扩展配置必须是 JSON 对象'); return }
		if (mqttSelected) parsed.mode = mqttServer ? 'server' : 'client'
		if (type === 'gree') {
			const greeConfigError = normalizeGreeConfig(parsed)
			if (greeConfigError) { setError(greeConfigError); setFieldErrors({ config: greeConfigError }); return }
		}
		if (!onTest) return
		setTesting(true); setError(null); setTestResult(null)
			try { await onTest({ id, name, type: providerType, enabled, config: parsed }); setTestResult(type === 'xiaomi-miot-cloud' ? '云账号登录成功，设备目录与 MIoT 属性接口可用。' : type === 'gree' ? 'Gree 局域网空调连接测试成功。' : type === 'network' ? '网络设备探测测试成功，已验证当前配置的可达性。' : type === 'tuya' ? 'Tuya 云账号连接测试成功，设备目录可用。' : mqttSelected ? mqttServer ? 'MQTT 服务端监听测试成功。保存 Provider 后外部设备可连接此地址。' : 'MQTT 客户端已连接外部 Broker。保存 Provider 后再配置设备 Topic。' : '连接成功，订阅已建立。') } catch (cause) { setError(cause instanceof Error ? cause.message : '连接测试失败') } finally { setTesting(false) }
		}
		const sonoffConfiguration = <div className="wide xiaomi-connection-flow">
			<section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>eWeLink 账号登录</strong><small>参考 SonoffLAN：登录后读取云端设备目录和 devicekey，局域网优先时不需要手工复制 Access Token。</small></div></div><div className="mqtt-config-grid">
				<label>运行模式<select aria-label="Sonoff 运行模式" value={String(configObject.mode ?? 'auto')} onChange={(event) => setConfig(JSON.stringify({ ...configObject, mode: event.target.value }, null, 2))}><option value="auto">Auto（局域网优先，云端回退）</option><option value="local">Local（仅局域网）</option><option value="cloud">Cloud（仅云端）</option></select></label>
				<label>账号国家区号<input aria-label="eWeLink 国家区号" value={String(sonoffCloud.countryCode ?? '+86')} onChange={(event) => updateSonoffCloud('countryCode', event.target.value)} placeholder="+86" /></label>
				<label>eWeLink 邮箱 / 手机号<input aria-label="eWeLink 账号" value={String(sonoffCloud.username ?? '')} onChange={(event) => updateSonoffCloud('username', event.target.value)} autoComplete="username" placeholder="邮箱或手机号" />{!String(sonoffCloud.username ?? '').includes('@') && <small>手机号不必重复填写国家区号；选择 +86 后输入 13800138000。</small>}</label>
				<label>eWeLink 密码<input aria-label="eWeLink 密码" type="password" value={String(sonoffCloud.password ?? '')} onChange={(event) => updateSonoffCloud('password', event.target.value)} autoComplete="current-password" />{hasRedactedSecrets && <small>保持 ******** 可沿用数据库中的加密密码。</small>}</label>
				<button type="button" className="example-button" disabled={sonoffAuthenticating || saving} onClick={() => void beginSonoffLogin()}>{sonoffAuthenticating ? '正在登录 eWeLink…' : String(sonoffCloud.accessToken ?? '').trim() ? '重新登录并刷新设备密钥' : '登录 eWeLink 账号'}</button>
				{String(sonoffCloud.accessToken ?? '').trim() && <small className="test-success">eWeLink 会话已就绪。保存后 Provider 会复用 Token；密码用于 Token 失效时自动重新登录。</small>}
				<label>请求超时（秒）<input aria-label="Sonoff 请求超时" type="number" min="1" max="120" value={Number(configObject.requestTimeoutSeconds ?? 10)} onChange={(event) => setConfig(JSON.stringify({ ...configObject, requestTimeoutSeconds: Number(event.target.value) }, null, 2))} /></label>
				<label>刷新间隔（秒）<input aria-label="Sonoff 刷新间隔" type="number" min="15" max="86400" value={Number(configObject.refreshIntervalSeconds ?? 60)} onChange={(event) => setConfig(JSON.stringify({ ...configObject, refreshIntervalSeconds: Number(event.target.value) }, null, 2))} /></label>
				<label>局域网扫描时长（秒）<input aria-label="Sonoff 局域网扫描时长" type="number" min="1" max="30" value={Number(configObject.discoveryTimeoutSeconds ?? 5)} onChange={(event) => setConfig(JSON.stringify({ ...configObject, discoveryTimeoutSeconds: Number(event.target.value) }, null, 2))} /></label>
			</div></section>
			<div className="xiaomi-next-step"><strong>02 · 管理易微联设备</strong><p>保存并启用 Provider 后，从 Provider 卡片进入“管理设备”；在那里合并 eWeLink 云目录、局域网扫描和已保存清单。只有加入受管清单并保存的设备才会稳定保留在发布者下面。</p></div>
			<details><summary>云端端点与已有 Token（高级）</summary><div className="mqtt-tls-grid"><label>云端区域<select aria-label="Sonoff 云端区域" value={String(configObject.region ?? 'auto')} onChange={(event) => setConfig(JSON.stringify({ ...configObject, region: event.target.value }, null, 2))}><option value="auto">自动</option><option value="cn">中国（cn）</option><option value="as">亚洲（as）</option><option value="us">美国（us）</option><option value="eu">欧洲（eu）</option></select></label><label>Endpoint<input aria-label="Sonoff 云端 Endpoint" value={String(sonoffCloud.endpoint ?? '')} onChange={(event) => updateSonoffCloud('endpoint', event.target.value)} placeholder="留空按区域选择" /></label><label>Access Token<input aria-label="Sonoff Access Token" type="password" value={String(sonoffCloud.accessToken ?? '')} onChange={(event) => updateSonoffCloud('accessToken', event.target.value)} /></label><label>WebSocket Endpoint（可选）<input aria-label="Sonoff WebSocket Endpoint" value={String(sonoffCloud.websocketEndpoint ?? '')} onChange={(event) => updateSonoffCloud('websocketEndpoint', event.target.value)} placeholder="wss://…" /><small>仅填写已获授权且已验证的服务端点；Provider 使用当前会话 Bearer 认证接收状态帧。</small></label><label>自有 App ID（可选）<input aria-label="Sonoff App ID" value={String(sonoffCloud.appId ?? '')} onChange={(event) => updateSonoffCloud('appId', event.target.value)} /></label><label>自有 App Secret（可选）<input aria-label="Sonoff App Secret" type="password" value={String(sonoffCloud.appSecret ?? '')} onChange={(event) => updateSonoffCloud('appSecret', event.target.value)} /></label><small>通常不需要手工填写 Token 或 App 凭据；仅在使用自有 eWeLink 应用时覆盖默认兼容签名。</small></div></details>
			<details><summary>设备映射与完整 JSON（高级）</summary><label className="wide config-editor"><span>Sonoff Provider 配置 JSON</span><textarea aria-label="Sonoff Provider 高级配置" rows={9} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}<small>账号连接在这里配置；日常设备选择请使用保存后的“管理设备”页面。</small></label></details>
			{testResult && <small className="test-success">{testResult}</small>}{error && <small className="inline-error">{error}</small>}
		</div>
		const tuyaConfiguration = <div className="wide xiaomi-connection-flow">
			<section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>Tuya 登录方式</strong><small>默认使用 Home Assistant 同款的 User Code + Tuya/Smart Life App 扫码登录，不需要开发者账号。</small></div></div><div className="mqtt-config-grid">
				<label className="wide">登录方式<select aria-label="Tuya 登录方式" value={tuyaAuthType} onChange={(event) => updateTuya('authType', event.target.value)}><option value="sharing">Home Assistant 扫码（推荐）</option><option value="openapi">Tuya OpenAPI OAuth / 手动凭据</option></select></label>
				<label>云区域<select aria-label="Tuya 云区域" value={String(configObject.region ?? 'cn')} onChange={(event) => updateTuya('region', event.target.value)}><option value="cn">中国（cn）</option><option value="us">美国（us）</option><option value="eu">欧洲（eu）</option><option value="in">印度（in）</option><option value="sg">新加坡/亚太（sg）</option></select></label>
				<label>用户 UID<input aria-label="Tuya 用户 UID" value={String(configObject.uid ?? '')} onChange={(event) => updateTuya('uid', event.target.value)} placeholder="扫码授权后自动填写；手动模式必填" /></label>
				{tuyaAuthType === 'sharing' ? <label className="wide">Tuya User Code<input aria-label="Tuya User Code" required value={String(configObject.userCode ?? '')} onChange={(event) => updateTuya('userCode', event.target.value)} placeholder="Tuya/Smart Life App：我 → 设置 → 账号与安全" /><small>这是 App 里的 User Code，不是 Tuya IoT Platform 的 Access ID。</small></label> : <>
					<label>Access ID<input aria-label="Tuya Access ID" required value={String(configObject.accessId ?? '')} onChange={(event) => updateTuya('accessId', event.target.value)} autoComplete="username" /></label>
					<label>Access Secret<input aria-label="Tuya Access Secret" required type="password" value={String(configObject.accessSecret ?? '')} onChange={(event) => updateTuya('accessSecret', event.target.value)} autoComplete="new-password" />{hasRedactedSecrets && <small>保持 ******** 可沿用数据库中的 Secret。</small>}</label>
				</>}
				<label>轮询间隔（秒）<input aria-label="Tuya 轮询间隔" type="number" min="30" max="86400" value={Number(configObject.pollIntervalSeconds ?? 60)} onChange={(event) => updateTuya('pollIntervalSeconds', Number(event.target.value))} /><small>默认 60 秒；设备规格会缓存，每轮只读取轻量状态。</small></label>
				<label>请求超时（秒）<input aria-label="Tuya 请求超时" type="number" min="1" max="120" value={Number(configObject.requestTimeoutSeconds ?? 15)} onChange={(event) => updateTuya('requestTimeoutSeconds', Number(event.target.value))} /></label>
				{tuyaAuthType === 'openapi' && <><label className="wide">自定义 API 地址（可选）<input aria-label="Tuya API 地址" value={String(configObject.baseUrl ?? '')} onChange={(event) => updateTuya('baseUrl', event.target.value)} placeholder="留空按区域自动选择 https://openapi.tuya..." /></label><label className="wide">OAuth H5 授权页 URL<input aria-label="Tuya OAuth 授权页 URL" value={String(configObject.authorizationUrl ?? '')} onChange={(event) => updateTuya('authorizationUrl', event.target.value)} placeholder="从 Tuya IoT Platform 获取 H5 授权页 URL" /><small>可选的 OAuth H5 兼容流程；Home Assistant 扫码方式不需要此项。</small></label><label className="wide">OAuth 回调地址<input aria-label="Tuya OAuth 回调地址" value={String(configObject.redirectUrl ?? tuyaOAuthDefaultRedirectURL)} onChange={(event) => updateTuya('redirectUrl', event.target.value)} /><small>默认回调：{tuyaOAuthDefaultRedirectURL}。</small></label></>}
			</div></section>
			{tuyaAuthType === 'sharing' ? <><div className="config-note"><span>Home Assistant 兼容扫码</span><strong>使用 Tuya/Smart Life App 扫描二维码</strong><p>输入 App 中的 User Code 后生成二维码，扫码确认后会自动回填 UID、Token 和设备云端 API 地址。</p></div><button type="button" className="example-button" disabled={tuyaSharingBusy || saving} onClick={() => void beginTuyaSharingLogin()}>{tuyaSharingBusy ? '正在生成二维码…' : '获取 Tuya 扫码二维码'}</button>{tuyaSharingSession && <section className="wide xiaomi-connection-flow" role="region" aria-label="Tuya Home Assistant 扫码授权"><div className="xiaomi-oauth-callback"><strong>请使用 Tuya/Smart Life App 扫描二维码</strong><img src={tuyaSharingQRCodeURL(tuyaSharingSession.state)} alt="Tuya Home Assistant 扫码二维码" /><small>二维码有效期至 {new Date(tuyaSharingSession.expiresAt).toLocaleTimeString()}；页面会自动等待手机确认。</small></div></section>}</> : <><div className="config-note"><span>OpenAPI OAuth</span><strong>使用 Tuya OAuth H5 授权页</strong><p>此模式需要 Tuya IoT Platform 的 Access ID/Secret，适合已有开发者项目的配置。</p></div><button type="button" className="example-button" disabled={tuyaOAuthBusy || saving} onClick={() => void beginTuyaOAuth()}>{tuyaOAuthBusy ? '正在准备 Tuya 授权…' : '开始 Tuya OAuth 授权'}</button>{tuyaOAuthSession && <section className="wide xiaomi-connection-flow" role="region" aria-label="Tuya OAuth 授权"><div className="xiaomi-oauth-callback"><strong>请使用 Tuya/Smart Life App 扫描二维码</strong><img src={tuyaOAuthQRCodeURL(tuyaOAuthSession.state)} alt="Tuya OAuth 扫码授权二维码" /><small>授权页有效期至 {new Date(tuyaOAuthSession.expiresAt).toLocaleTimeString()}。</small><a href={tuyaOAuthSession.authorizationUrl} target="_blank" rel="noreferrer">打开 Tuya 授权页</a><textarea aria-label="Tuya OAuth 回调 URL" rows={3} value={tuyaOAuthCallbackURL} onChange={(event) => setTuyaOAuthCallbackURL(event.target.value)} placeholder="扫码授权后若未自动返回，请粘贴完整回调 URL" spellCheck={false} /><button type="button" disabled={tuyaOAuthBusy || !tuyaOAuthCallbackURL.trim()} onClick={() => void completeTuyaAuthorization()}>{tuyaOAuthBusy ? '正在换取 Token…' : '解析回调并完成授权'}</button></div></section>}</>}
			<details><summary>Tuya MQTT / DP 修补（高级 JSON）</summary><label className="wide config-editor"><span>Tuya 扩展配置（JSON）</span><textarea aria-label="Tuya 扩展配置 JSON" aria-invalid={Boolean(fieldErrors.config)} rows={11} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}<small>可在此配置已授权的 MQTT 凭据（mqtt）或产品 DP 兼容修补（quirks）；日常接入只需填写上方账号字段。</small></label></details>
			{onTest && <button type="button" className="example-button" disabled={testing || saving} onClick={() => void testConnection()}>{testing ? '连接测试中…' : '测试 Tuya 连接'}</button>}{testResult && <small className="wide test-success">{testResult}</small>}
		</div>
		const greeConfiguration = <div className="wide xiaomi-connection-flow">
			<section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>创建 Gree 局域网 Provider</strong><small>Provider 只负责局域网连接和轮询；保存后从 Provider 卡片进入“管理格力设备”，逐台扫描或添加空调。</small></div></div><div className="mqtt-config-grid">
				<label>轮询间隔（pollIntervalSeconds）<input aria-label="Gree 轮询间隔" type="number" min="1" max="3600" value={configObject.pollIntervalSeconds === '' ? '' : Number(configObject.pollIntervalSeconds ?? 60)} onChange={(event) => updateGreeRuntime('pollIntervalSeconds', event.target.value === '' ? '' : Number(event.target.value))} /><small>Provider 全局轮询间隔，默认 60 秒。</small></label>
				<label>请求超时（requestTimeoutSeconds）<input aria-label="Gree 请求超时" type="number" min="1" max="120" value={configObject.requestTimeoutSeconds === '' ? '' : Number(configObject.requestTimeoutSeconds ?? 5)} onChange={(event) => updateGreeRuntime('requestTimeoutSeconds', event.target.value === '' ? '' : Number(event.target.value))} /><small>Provider 全局请求超时，默认 5 秒。</small></label>
			</div></section>
			<div className="config-note"><span>下一步</span><strong>保存后管理格力设备</strong><p>Provider 保存并进入 running 后，卡片会显示“管理格力设备”；在那里可以扫描 UDP/7000、手动添加、编辑和移除多台空调。</p></div>
			<details><summary>格力设备 JSON（高级兼容入口）</summary><label className="wide config-editor"><span>Gree devices 配置（JSON）</span><textarea aria-label="Gree 设备配置 JSON" aria-invalid={Boolean(fieldErrors.config)} rows={11} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}<small>日常添加设备请使用 Provider 卡片中的管理器；此处仅用于导入历史配置。每项支持 host、port、mac、name，以及可选 encryptionKey、encryptionVersion。</small></label></details>
			{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{hasRedactedSecrets && <small>敏感字段已显示为 ********；保持占位符即可沿用数据库中的原值。</small>}{testResult && <small className="test-success">{testResult}</small>}
		</div>
		const networkConfiguration = <div className="wide xiaomi-connection-flow">
			<section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>配置局域网设备监测</strong><small>可通过 TCP 端口或 ICMP 回显确认设备电源状态；探测失败会显示为已关闭。配置 MAC 后，开启操作会发送 Wake-on-LAN 魔术包，控制会立即确认，实际开机仍由后续探测确认。</small></div></div><div className="mqtt-config-grid">
				<label>默认探测方式（probeMethod）<select aria-label="网络设备默认探测方式" value={String(configObject.probeMethod ?? 'tcp')} onChange={(event) => updateNetworkRuntime('probeMethod', event.target.value)}><option value="tcp">TCP 端口</option><option value="icmp">ICMP Ping</option></select><small>TCP 适合确认指定服务；ICMP 不需要端口，适合只判断主机是否可达。</small></label>
				<label>探测间隔（probeIntervalSeconds）<input aria-label="网络设备探测间隔" type="number" min="1" max="3600" value={Number(configObject.probeIntervalSeconds ?? 30)} onChange={(event) => updateNetworkRuntime('probeIntervalSeconds', Number(event.target.value))} /><small>所有未单独覆盖的设备按此间隔探测，默认 30 秒。</small></label>
				<label>探测超时（probeTimeoutSeconds）<input aria-label="网络设备探测超时" type="number" min="1" max="120" value={Number(configObject.probeTimeoutSeconds ?? 3)} onChange={(event) => updateNetworkRuntime('probeTimeoutSeconds', Number(event.target.value))} /><small>TCP 建连或 ICMP 回显最长等待时间，默认 3 秒。</small></label>
				<label>唤醒确认宽限（wakeGraceSeconds）<input aria-label="网络设备唤醒确认宽限" type="number" min="5" max="3600" value={Number(configObject.wakeGraceSeconds ?? 300)} onChange={(event) => updateNetworkRuntime('wakeGraceSeconds', Number(event.target.value))} /><small>魔术包发出后显示“启动中”的最长时间，默认 300 秒；这不属于控制超时。</small></label>
				<label>开启阈值（onlineThreshold）<input aria-label="网络设备在线阈值" type="number" min="1" max="100" value={Number(configObject.onlineThreshold ?? 1)} onChange={(event) => updateNetworkRuntime('onlineThreshold', Number(event.target.value))} /><small>连续成功达到阈值后才显示已开启。</small></label>
				<label>关闭阈值（offlineThreshold）<input aria-label="网络设备离线阈值" type="number" min="1" max="100" value={Number(configObject.offlineThreshold ?? 2)} onChange={(event) => updateNetworkRuntime('offlineThreshold', Number(event.target.value))} /><small>连续失败达到阈值后才显示已关闭。</small></label>
				<label>WOL 广播地址（wolBroadcastAddress）<input aria-label="网络设备 WOL 广播地址" value={String(configObject.wolBroadcastAddress ?? '255.255.255.255')} onChange={(event) => updateNetworkRuntime('wolBroadcastAddress', event.target.value)} placeholder="255.255.255.255" /></label>
				<label>WOL 端口（wolPort）<input aria-label="网络设备 WOL 端口" type="number" min="1" max="65535" value={Number(configObject.wolPort ?? 9)} onChange={(event) => updateNetworkRuntime('wolPort', Number(event.target.value))} /></label>
				<label className="wide">WOL 网络接口（wolInterface，可选）<input aria-label="网络设备 WOL 网络接口" value={String(configObject.wolInterface ?? '')} onChange={(event) => updateNetworkRuntime('wolInterface', event.target.value)} placeholder="例如 eth0；留空由系统选择" /></label>
			</div></section>
			<div className="xiaomi-next-step"><strong>02 · 从统一设备管理入口添加</strong><p>Provider 可以先以空设备目录保存并运行。随后从 Provider 卡片进入“管理网络设备”，逐台填写稳定 ID、Host、MAC 和可选覆盖项，加入草稿后统一保存设备并实时应用。</p></div>
			<div className="config-note"><span>状态与唤醒规则</span><strong>魔术包发出后等待 TCP 或 ICMP 确认可达</strong><p>启动期间显示“启动中”，并以最多每 10 秒一次的探测确认；超过唤醒确认宽限仍未可达时恢复为已关闭。设备级覆盖在“管理网络设备”页中设置。</p></div>
			<details><summary>网络 Provider 高级 JSON（迁移/批量导入）</summary><label className="wide config-editor"><span>网络 Provider 配置 JSON</span><textarea aria-label="网络 Provider 配置 JSON" aria-invalid={Boolean(fieldErrors.config)} rows={14} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}<small>日常添加、编辑或删除设备请使用 Provider 卡片的“管理网络设备”；此入口只用于迁移历史配置或批量导入。</small><button type="button" className="example-button" onClick={() => setConfig(JSON.stringify(networkExample, null, 2))}>载入网络 Provider 示例</button></label></details>
			{onTest && <button type="button" className="example-button" disabled={testing || saving || !Array.isArray(configObject.devices) || configObject.devices.length === 0} onClick={() => void testConnection()}>{testing ? '正在探测…' : '测试已添加的网络设备'}</button>}{testResult && <small className="wide test-success">{testResult}</small>}
		</div>
		return <div className="modal-backdrop"><form className="target-form" role="dialog" aria-modal="true" aria-labelledby="provider-form-title" onSubmit={(event) => void submit(event)}>
		<div className="form-heading"><div><p className="eyebrow">PROVIDER</p><h2 id="provider-form-title">{provider ? '编辑 Provider' : '新建 Provider'}</h2></div><button type="button" onClick={onCancel}>关闭</button></div>
			<div className="form-grid"><label>ID（留空自动生成）<input aria-invalid={Boolean(fieldErrors.id)} value={id} disabled={Boolean(provider)} onChange={(event) => setID(event.target.value)} placeholder={mqttSelected ? (mqttServer ? 'mqtt-server-main' : 'mqtt-client-main') : type === 'xiaomi' ? 'xiaomi-main' : type === 'xiaomi-miot-cloud' ? 'xiaomi-miot-cloud-main' : type === 'gree' ? 'gree-main' : type === 'network' ? 'network-main' : type === 'tuya' ? 'tuya-main' : type === 'sonoff' ? 'sonoff-main' : type === 'camera' ? 'camera-main' : 'virtual-lab'} />{fieldErrors.id && <small className="field-error">{fieldErrors.id}</small>}</label><label>名称<input aria-invalid={Boolean(fieldErrors.name)} value={name} onChange={(event) => setName(event.target.value)} placeholder={mqttSelected ? (mqttServer ? '家庭 MQTT 服务端' : '家庭 MQTT 客户端') : type === 'xiaomi' ? '米家中枢网关' : type === 'xiaomi-miot-cloud' ? '小米 MIoT 云（第三方兼容）' : type === 'gree' ? '客厅格力空调' : type === 'network' ? '局域网设备监测' : type === 'tuya' ? '涂鸦云设备' : type === 'sonoff' ? 'Sonoff/eWeLink 设备' : type === 'camera' ? '家庭摄像头' : '实验室虚拟设备'} />{fieldErrors.name && <small className="field-error">{fieldErrors.name}</small>}</label><label className="wide">类型<select aria-invalid={Boolean(fieldErrors.type)} value={type} disabled={Boolean(provider)} onChange={(event) => { const next = event.target.value as ProviderSelection; setType(next); if (!provider) { const selected = next === 'mqtt-client' ? createMQTTExample('client') : next === 'mqtt-server' ? createMQTTExample('server') : next === 'xiaomi' ? xiaomiExample : next === 'xiaomi-miot-cloud' ? xiaomiCloudExample : next === 'gree' ? greeExample : next === 'network' ? networkExample : next === 'tuya' ? initialTuyaConfig : next === 'sonoff' ? initialSonoffConfig : next === 'camera' ? createCameraExample() : createVirtualExample(); setConfig(JSON.stringify(selected, null, 2)) } }}><option value="virtual">Virtual</option><option value="camera">Camera（独立摄像头来源）</option><option value="mqtt-client">MQTT Client（客户端 · 连接外部 Broker）</option><option value="mqtt-server">MQTT Server（服务端 · 接受设备连接）</option><option value="xiaomi">Xiaomi Central Hub（中枢网关）</option><option value="xiaomi-miot-cloud">Xiaomi MIoT Cloud（账号与设备目录）</option><option value="gree">Gree 局域网空调</option><option value="network">网络设备监测 / Wake-on-LAN</option><option value="tuya">Tuya 涂鸦云（设备目录）</option><option value="sonoff">Sonoff/eWeLink（局域网优先）</option></select>{fieldErrors.type && <small className="field-error">{fieldErrors.type}</small>}</label>
	{mqttSelected ? <div className="wide mqtt-config-grid">
		<div className="wide config-note"><span>MQTT Provider</span><strong>{mqttServer ? 'MQTT Server · 服务端' : 'MQTT Client · 客户端'}</strong><p>{mqttServer ? 'HomeLoom 内嵌 Broker 并监听 TCP 地址；设备主动连接 HomeLoom。' : 'HomeLoom 作为客户端连接现有 Mosquitto、EMQX 或其他 Broker。'}</p></div>
		{mqttServer ? <>
		<label>监听地址（listenAddress）<input aria-label="MQTT 服务端监听地址" required value={String(configObject.listenAddress ?? '')} onChange={(event) => updateMQTT('listenAddress', event.target.value)} placeholder="0.0.0.0:1883" /></label>
		<label>设备用户名<input aria-label="MQTT 用户名" value={String(configObject.username ?? '')} onChange={(event) => updateMQTT('username', event.target.value)} autoComplete="username" placeholder="建议配置" /></label>
		<label>设备密码<input aria-label="MQTT 密码" type="password" value={String(configObject.password ?? '')} onChange={(event) => updateMQTT('password', event.target.value)} autoComplete="new-password" placeholder="建议配置" />{hasRedactedSecrets && <small>保持 ******** 可沿用数据库中的密码。</small>}</label>
		<label>启动超时（秒）<input aria-label="MQTT 连接超时" type="number" min="1" max="120" value={Number(configObject.connectTimeoutSeconds ?? 10)} onChange={(event) => updateMQTT('connectTimeoutSeconds', Number(event.target.value))} /></label>
		<label>Retained State 最大年龄（秒）<input aria-label="MQTT Retained State 最大年龄" type="number" min="1" max="86400" value={Number(configObject.retainedStateMaxAgeSeconds ?? 300)} onChange={(event) => updateMQTT('retainedStateMaxAgeSeconds', Number(event.target.value))} /></label>
		<details className="wide"><summary>TLS / mTLS 服务端设置</summary><div className="mqtt-tls-grid"><label>服务端证书<input aria-label="MQTT 服务端证书" value={String(tlsConfig.certFile ?? '')} onChange={(event) => updateTLS('certFile', event.target.value)} /></label><label>服务端私钥<input aria-label="MQTT 服务端私钥" value={String(tlsConfig.keyFile ?? '')} onChange={(event) => updateTLS('keyFile', event.target.value)} /></label><label>客户端 CA（启用 mTLS）<input aria-label="MQTT 客户端 CA 文件" value={String(tlsConfig.caFile ?? '')} onChange={(event) => updateTLS('caFile', event.target.value)} /><small>填写后要求设备提供由该 CA 签发的客户端证书。</small></label></div></details>
		</> : <>
		<label>外部 Broker URL<input aria-label="MQTT Broker URL" required value={String(configObject.brokerUrl ?? '')} onChange={(event) => updateMQTT('brokerUrl', event.target.value)} placeholder="mqtt://192.168.1.10:1883" /></label>
		<label>用户名<input aria-label="MQTT 用户名" value={String(configObject.username ?? '')} onChange={(event) => updateMQTT('username', event.target.value)} autoComplete="username" /></label>
		<label>密码<input aria-label="MQTT 密码" type="password" value={String(configObject.password ?? '')} onChange={(event) => updateMQTT('password', event.target.value)} autoComplete="new-password" />{hasRedactedSecrets && <small>保持 ******** 可沿用数据库中的密码。</small>}</label>
		<label>Client ID<input aria-label="MQTT Client ID" value={String(configObject.clientId ?? '')} onChange={(event) => updateMQTT('clientId', event.target.value)} placeholder="留空自动生成" /></label>
		<label>Keep Alive（秒）<input aria-label="MQTT Keep Alive" type="number" min="5" max="3600" value={Number(configObject.keepAliveSeconds ?? 30)} onChange={(event) => updateMQTT('keepAliveSeconds', Number(event.target.value))} /></label>
		<label>连接超时（秒）<input aria-label="MQTT 连接超时" type="number" min="1" max="120" value={Number(configObject.connectTimeoutSeconds ?? 10)} onChange={(event) => updateMQTT('connectTimeoutSeconds', Number(event.target.value))} /></label>
		<label>Session 保留（秒）<input aria-label="MQTT Session 保留" type="number" min="1" value={Number(configObject.sessionExpirySeconds ?? 86400)} onChange={(event) => updateMQTT('sessionExpirySeconds', Number(event.target.value))} /></label>
		<label>Retained State 最大年龄（秒）<input aria-label="MQTT Retained State 最大年龄" type="number" min="1" max="86400" value={Number(configObject.retainedStateMaxAgeSeconds ?? 300)} onChange={(event) => updateMQTT('retainedStateMaxAgeSeconds', Number(event.target.value))} /></label>
		<details className="wide"><summary>TLS / mTLS 高级设置</summary><div className="mqtt-tls-grid"><label>CA 文件<input aria-label="MQTT CA 文件" value={String(tlsConfig.caFile ?? '')} onChange={(event) => updateTLS('caFile', event.target.value)} /></label><label>Server Name<input aria-label="MQTT TLS Server Name" value={String(tlsConfig.serverName ?? '')} onChange={(event) => updateTLS('serverName', event.target.value)} /></label><label>客户端证书<input aria-label="MQTT 客户端证书" value={String(tlsConfig.certFile ?? '')} onChange={(event) => updateTLS('certFile', event.target.value)} /></label><label>客户端私钥<input aria-label="MQTT 客户端私钥" value={String(tlsConfig.keyFile ?? '')} onChange={(event) => updateTLS('keyFile', event.target.value)} /></label><label className="enable-row"><input type="checkbox" checked={Boolean(tlsConfig.insecureSkipVerify)} onChange={(event) => updateTLS('insecureSkipVerify', event.target.checked)} />跳过证书校验（仅调试）</label></div></details>
		</>}
		{fieldErrors.config && <small className="field-error wide">{fieldErrors.config}</small>}<small className="wide">{mqttServer ? '这里启动内嵌 Broker 监听；未配置设备路由时 ACL 会拒绝全部设备 Topic。' : '这里建立到外部 Broker 的 MQTT 客户端连接。'}保存并启用后，从 Provider 卡片进入“管理设备”，逐台配置 Topic、QoS 和收发路由。配置保存到 PostgreSQL，密码由主密钥加密且由 API 脱敏返回。</small>
		{onTest && <button type="button" className="example-button" disabled={testing || saving} onClick={() => void testConnection()}>{testing ? (mqttServer ? '监听测试中…' : '连接中…') : mqttServer ? '测试监听' : '测试连接'}</button>}{testResult && <small className="wide test-success">{testResult}</small>}
		</div> : type === 'tuya' ? tuyaConfiguration : type === 'sonoff' ? sonoffConfiguration : type === 'xiaomi' ? <div className="wide xiaomi-connection-flow">
			<section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>OAuth 授权与证书</strong><small>先完成账号授权，由 HomeLoom 获取中枢 MQTT 客户端证书。</small></div></div><div className="mqtt-config-grid">
				<label>OAuth Client ID<input aria-label="小米 OAuth Client ID" required value={String(xiaomiOAuth.clientId ?? '')} onChange={(event) => updateXiaomiOAuth('clientId', event.target.value)} placeholder="必须使用有权使用的数值型 Client ID" /></label>
				<label>账号地区<select aria-label="小米账号地区" value={String(xiaomiOAuth.region ?? 'cn')} onChange={(event) => updateXiaomiOAuth('region', event.target.value)}><option value="cn">中国大陆</option><option value="de">欧洲</option><option value="us">美国</option><option value="ru">俄罗斯</option><option value="tw">台湾</option><option value="sg">新加坡</option><option value="in">印度</option></select></label>
				<label className="wide">OAuth Redirect URL<input aria-label="小米 OAuth Redirect URL" readOnly value={xiaomiOAuthRedirectURL} /><small>跳转后页面即使无法打开，也可以直接复制浏览器地址栏中的完整 URL。</small></label>
				<button type="button" className="example-button" disabled={authorizing || completingOAuth || saving} onClick={() => void authorizeXiaomi()}>{authorizing ? '正在打开授权页面…' : configObject.clientCertificate ? '重新授权并申请证书' : '打开小米授权页面'}</button>
				<div className="wide xiaomi-oauth-callback"><strong>粘贴授权后的完整 URL</strong><ol><li>在小米页面完成登录和授权。</li><li>跳转到 <code>{xiaomiOAuthRedirectURL}</code> 后复制完整 URL。</li><li>粘贴到下方，HomeLoom 校验 state 后换取 Token 和证书。</li></ol><textarea aria-label="小米 OAuth 回调 URL" rows={3} value={oauthCallbackURL} onChange={(event) => setOAuthCallbackURL(event.target.value)} placeholder={`${xiaomiOAuthRedirectURL}/?code=...&state=...`} spellCheck={false} /><button type="button" disabled={completingOAuth || !oauthCallbackURL.trim()} onClick={() => void completeXiaomiAuthorization()}>{completingOAuth ? '正在换取证书…' : '解析 URL 并完成授权'}</button></div>
			</div></section>
			<section className={`xiaomi-connection-step ${configObject.clientCertificate ? '' : 'is-locked'}`}><div className="xiaomi-connection-step__heading"><span>02</span><div><strong>连接中枢 MQTT</strong><small>{configObject.clientCertificate ? '证书已就绪。选择中枢、测试连接并保存 Provider。' : '完成 OAuth 和证书申请后开放此步骤。'}</small></div></div><div className="mqtt-config-grid">
				<label>中枢网关地址<input aria-label="小米中枢网关地址" required disabled={!configObject.clientCertificate} value={String(configObject.host ?? '')} onChange={(event) => updateXiaomi('host', event.target.value)} placeholder="192.168.1.50" /></label>
				<label>MQTT 端口<input aria-label="小米中枢网关端口" type="number" min="1" max="65535" disabled={!configObject.clientCertificate} value={Number(configObject.port ?? 8883)} onChange={(event) => updateXiaomi('port', Number(event.target.value))} /></label>
				<button type="button" className="example-button" disabled={discoveringGateways || !configObject.clientCertificate} onClick={() => void discoverGateways()}>{discoveringGateways ? '正在发现小米中枢网关…' : '发现小米中枢网关'}</button>
				<label>状态补偿间隔（秒）<input aria-label="小米状态补偿间隔" type="number" min="5" max="3600" disabled={!configObject.clientCertificate} value={Number(configObject.pollIntervalSeconds ?? 60)} onChange={(event) => updateXiaomi('pollIntervalSeconds', Number(event.target.value))} /><small>官方云 MQTT 与中枢推送为主；仅断线或属性不支持 notify 时使用 HTTP 补偿。</small></label>
				{gateways.length > 0 && <div className="wide"><small>选择支持 MQTT 的主中枢：</small><div className="simulation-actions">{gateways.map((gateway) => <button type="button" key={`${gateway.instance}-${gateway.hostName}`} disabled={!gateway.mqttEnabled} onClick={() => setConfig(JSON.stringify({ ...configObject, host: gateway.addresses[0] ?? gateway.hostName, port: gateway.port, gatewayDid: gateway.did ?? '' }, null, 2))}>{gateway.instance} · {gateway.addresses[0] ?? gateway.hostName} · DID {gateway.did ?? '未知'} · role {gateway.role ?? '—'}</button>)}</div></div>}
				{onTest && <button type="button" className="example-button" disabled={testing || saving || authorizing || !configObject.clientCertificate || !configObject.host} onClick={() => void testConnection()}>{testing ? '连接中…' : '测试中枢 MQTT'}</button>}
			</div></section>
			<div className="xiaomi-next-step"><strong>03 · 子设备配置</strong><p>保存并启用 Provider，确认 MQTT 状态为“running”后，在米家页面进入独立的“管理子设备”页面。这里不再读取或编辑设备映射。</p></div>
			<details><summary>连接与证书高级设置</summary><div className="mqtt-tls-grid"><label>Virtual DID<input aria-label="小米 Virtual DID" readOnly value={String(configObject.clientId ?? xiaomiOAuth.virtualDid ?? '')} /></label><label>TLS Server Name（仅用于 SNI）<input aria-label="小米 TLS Server Name" value={String(configObject.serverName ?? '')} onChange={(event) => updateXiaomi('serverName', event.target.value)} /><small>不会用于 DNS/IP SAN 校验。</small></label><label className="enable-row"><input type="checkbox" checked={Boolean(configObject.insecureSkipVerify)} onChange={(event) => updateXiaomi('insecureSkipVerify', event.target.checked)} />跳过服务端证书校验（仅诊断）</label><small>{configObject.clientCertificate ? '默认验证小米 CA 链、证书有效期和 ServerAuth；不验证 DNS/IP SAN。' : '尚未申请中枢客户端证书。'}</small></div></details>
			{hasRedactedSecrets && <small>Token 和私钥已由 API 脱敏；保持 ******** 即可沿用数据库中的加密值。</small>}{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{testResult && <small className="test-success">{testResult}</small>}
			</div> : type === 'xiaomi-miot-cloud' ? <div className="wide xiaomi-connection-flow">
				<section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>第三方兼容 MIoT 云账号</strong><small>此接口参考 hass-xiaomi-miot，并非预留的官方 Xiaomi Home Cloud Provider。</small></div></div><div className="mqtt-config-grid">
					<label>小米账号<input aria-label="小米 MIoT 云账号" required={!configObject.serviceToken} value={String(configObject.username ?? '')} onChange={(event) => updateXiaomiCloudIdentity('username', event.target.value)} autoComplete="username" placeholder="邮箱、手机号或小米账号" /></label>
					<label>账号密码<input aria-label="小米 MIoT 云密码" type="password" required={!configObject.serviceToken} value={String(configObject.password ?? '')} onChange={(event) => updateXiaomiCloudIdentity('password', event.target.value)} autoComplete="new-password" />{hasRedactedSecrets && <small>保持 ******** 可沿用数据库中的加密密码。</small>}</label>
					<label>账号地区<select aria-label="小米 MIoT 云地区" value={String(configObject.region ?? 'cn')} onChange={(event) => updateXiaomiCloudIdentity('region', event.target.value)}><option value="cn">中国大陆（cn）</option><option value="de">欧洲（de）</option><option value="us">美国（us）</option><option value="ru">俄罗斯（ru）</option><option value="tw">台湾（tw）</option><option value="sg">新加坡（sg）</option><option value="in">印度（in）</option><option value="i2">印度二区（i2）</option></select></label>
					<label>状态同步间隔（秒）<input aria-label="小米 MIoT 云轮询间隔" type="number" min="15" max="3600" value={Number(configObject.pollIntervalSeconds ?? 30)} onChange={(event) => updateXiaomi('pollIntervalSeconds', Number(event.target.value))} /><small>auto 设备优先通过局域网读取；失败时本轮回退云端。</small></label>
					<label>请求超时（秒）<input aria-label="小米 MIoT 云请求超时" type="number" min="1" max="120" value={Number(configObject.requestTimeoutSeconds ?? 15)} onChange={(event) => updateXiaomi('requestTimeoutSeconds', Number(event.target.value))} /></label>
					<button type="button" className="example-button" disabled={cloudAuthenticating || saving} onClick={() => void beginCloudLogin()}>{cloudAuthenticating ? '正在登录…' : cloudSessionReady ? '重新登录小米云账号' : '登录小米云账号'}</button>
					{cloudSessionReady && cloudMediaSessionReady && <small className="test-success">云会话已就绪。保存后 Provider 将直接复用此会话，不会重复登录。</small>}
					{cloudSessionReady && !cloudMediaSessionReady && <small className="inline-error">MIoT 会话可用于普通设备；摄像头还需要使用账号密码重新登录以取得 passToken。</small>}
					{providerChallengeUnavailable && !cloudChallenge && <div role="alert" className="wide inline-error"><strong>此前的短信验证会话已经失效</strong><p>短信验证码只能提交到创建它的短时登录会话；请将密码框中的 `********` 替换为真实密码，点击“重新登录小米云账号”。新会话创建后，验证码输入框会显示在这里。</p></div>}
					{cloudChallenge && <div className="wide xiaomi-oauth-callback"><strong>{cloudChallengeSource === 'provider' ? 'Provider 启动需要短信验证' : '需要短信或邮件验证'}</strong><ol>{cloudChallenge.verificationUrl && <li><a href={cloudChallenge.verificationUrl} target="_blank" rel="noreferrer">打开小米身份验证页面</a>；在小米页面选择手机号或邮箱并发送验证码。</li>}<li>收到验证码后回到 HomeLoom，在下方填写并提交；收到后不要在小米页面提交验证码。</li></ol>{cloudChallengeExpired ? <div role="alert" className="inline-error">小米验证会话已过期，请重新登录后再次获取验证码。</div> : <><label>短信 / 邮件验证码<input aria-label="小米 MIoT 云验证码" inputMode="numeric" autoComplete="one-time-code" value={cloudVerificationCode} onChange={(event) => setCloudVerificationCode(event.target.value)} /></label><button type="button" disabled={cloudAuthenticating || !cloudVerificationCode.trim()} onClick={() => void completeCloudVerification()}>{cloudAuthenticating ? '正在验证…' : cloudChallengeSource === 'provider' ? '提交验证码并继续 Provider' : '提交验证码并继续登录'}</button></>}{cloudChallenge.message && <small>{cloudChallenge.message}</small>}{cloudChallenge.expiresAt && cloudChallengeExpiresAt !== null && <small>此登录会话将在 {new Date(cloudChallengeExpiresAt).toLocaleTimeString()} 过期；过期后请重新登录。</small>}</div>}
					{onTest && cloudSessionReady && <button type="button" className="example-button" disabled={testing || saving || cloudAuthenticating} onClick={() => void testConnection()}>{testing ? '正在读取…' : '测试 MIoT 云连接'}</button>}
				</div></section>
				<div className="xiaomi-next-step"><strong>02 · 保存并选择云端设备</strong><p>登录完成后保存并启用 Provider，再从 Provider 卡片进入“管理设备”；系统复用当前云会话读取账号设备目录，不会重复登录。</p></div>
				<details><summary>已有会话凭据（高级替代方案）</summary><div className="mqtt-tls-grid"><label>User ID（userId）<input aria-label="小米 MIoT 云 User ID" value={String(configObject.userId ?? '')} onChange={(event) => updateXiaomi('userId', event.target.value)} /></label><label>ssecurity<input aria-label="小米 MIoT 云 ssecurity" type="password" value={String(configObject.ssecurity ?? '')} onChange={(event) => updateXiaomi('ssecurity', event.target.value)} /></label><label>Service Token（serviceToken）<input aria-label="小米 MIoT 云 Service Token" type="password" value={String(configObject.serviceToken ?? '')} onChange={(event) => updateXiaomi('serviceToken', event.target.value)} /></label><label>Camera Pass Token（passToken）<input aria-label="小米 MIoT 云 Camera Pass Token" type="password" value={String(configObject.passToken ?? '')} onChange={(event) => updateXiaomi('passToken', event.target.value)} /></label><small>普通 MIoT 设备需要前三项；启用小米摄像头时还需要 passToken。凭据会加密保存，会话过期后需重新登录。</small></div></details>
				<small>密码与云会话 Token 保存到 PostgreSQL 并加密。设备局域网 Token 仅在后端云目录运行时使用，不通过管理 API 返回；没有本地能力的设备继续使用云轮询。该接口不适合无线开关、人体传感器等瞬时事件设备。</small>
				{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{testResult && <small className="test-success">{testResult}</small>}
			</div> : type === 'gree' ? greeConfiguration : type === 'network' ? networkConfiguration : type === 'camera' ? <div className="wide xiaomi-connection-flow"><section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>创建 Camera Provider</strong><small>Camera Provider 建立独立媒体运行时边界；保存并启用后才启动 Media Worker。</small></div></div><div className="config-note"><span>子设备为空</span><strong>Camera Kernel 按 Provider 生命周期启停</strong><p>这里不再内嵌摄像头连接详情。保存 Provider 后，从 Provider 卡片进入“管理摄像头”，再逐台添加 RTSP、ONVIF 或 Xiaomi MISS 子设备。</p></div></section><div className="xiaomi-next-step"><strong>02 · 添加摄像头子设备</strong><p>Provider 状态变为 running 后进入独立子设备页面；保存子设备会重建媒体目录并通过 replay 实时应用。</p></div>{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}</div> : type === 'virtual' ? <div className="wide xiaomi-connection-flow"><section className="xiaomi-connection-step"><div className="xiaomi-connection-step__heading"><span>01</span><div><strong>创建 Virtual Provider</strong><small>Virtual Provider 只负责模拟运行时；保存并启用后，再从 Provider 卡片可视化添加虚拟子设备。</small></div></div><div className="config-note"><span>子设备为空</span><strong>先创建 Provider，再添加虚拟设备</strong><p>这里不直接编辑设备数组。Provider 进入 running 后，进入“管理虚拟设备”选择统一模型、填写名称和初始状态，保存即可实时重建运行目录。</p></div></section><div className="xiaomi-next-step"><strong>02 · 可视化添加虚拟子设备</strong><p>可创建开关、灯泡、传感器、风扇、净化器、窗帘、电视等内置模型；运行时模拟仍在 Provider 卡片中使用。</p></div><details><summary>Provider 高级配置（JSON）</summary><label className="wide config-editor"><span>扩展配置（JSON）</span><textarea aria-label="Virtual Provider 高级配置" aria-invalid={Boolean(fieldErrors.config)} rows={8} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}<small>高级配置仅用于调整 latencyMs、rejectWrites 或导入历史设备列表；日常添加子设备请使用“管理虚拟设备”。</small><button type="button" className="example-button" onClick={() => setConfig(JSON.stringify(example, null, 2))}>载入完整模型示例</button></label></details></div> : <label className="wide config-editor"><span>扩展配置（JSON）</span><textarea aria-invalid={Boolean(fieldErrors.config)} rows={11} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{hasRedactedSecrets && <small>敏感字段已显示为 ********；保持占位符即可沿用数据库中的原值，输入新值可进行替换。</small>}<small>支持 38 种内置统一模型及必须/可选参数；未进入标准契约的属性会标记为自定义参数。</small><button type="button" className="example-button" onClick={() => setConfig(JSON.stringify(example, null, 2))}>载入完整模型示例</button></label>}</div>
    {type === 'virtual' && hasRedactedSecrets && <small>敏感字段已显示为 ********；保持占位符即可沿用数据库中的原值，输入新值可进行替换。</small>}
    <label className="enable-row"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />立即启用（无需重启服务）</label>
	{error && <p role="alert" className="inline-error">{error}</p>}<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '应用中…' : '保存并应用'}</button></div>
  </form></div>
}
