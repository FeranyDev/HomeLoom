import { useEffect, useState } from 'react'
import { availabilityLabel, deviceProperty, runtimeModeLabel, type Device, type DeviceAvailability } from '../types/device'
import type { Provider, ProviderAuthChallenge, ProviderInput } from '../types/provider'
import { getXiaomiProviderAuthChallenge, verifyXiaomiProviderAuthChallenge } from '../api/xiaomi'
import { supportsProviderChildDevices } from '../providerRouting'

type SimulationValues = { availability?: DeviceAvailability; online?: boolean; power?: boolean; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; active?: boolean; speed?: number; mode?: string; filterLife?: number; filterChange?: boolean; position?: number; sequence?: number; repeat?: number }

function propertyBool(device: Device, capability: string, property: string): boolean { return deviceProperty(device, capability, property)?.bool ?? false }
function propertyNumber(device: Device, capability: string, property: string): number { return deviceProperty(device, capability, property)?.number ?? 0 }
function propertyInt(device: Device, capability: string, property: string): number { return deviceProperty(device, capability, property)?.int ?? 0 }
function propertyString(device: Device, capability: string, property: string): string { return deviceProperty(device, capability, property)?.string ?? '' }
function objectValue(value: unknown): Record<string, unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {} }
function arrayValue(value: unknown): unknown[] { return Array.isArray(value) ? value : [] }
function dateTime(value?: string): string { return value ? new Date(value).toLocaleString() : '未知' }

function normalizeAuthChallenge(value: unknown): ProviderAuthChallenge | null {
	const source = objectValue(value)
	const nested = objectValue(source.challenge ?? source.authChallenge ?? source.auth_challenge)
	const raw = Object.keys(nested).length > 0 ? nested : source
	const challengeId = String(raw.challengeId ?? raw.challenge_id ?? raw.id ?? '').trim()
	if (!challengeId) return null
	const verificationUrl = String(raw.verificationUrl ?? raw.verification_url ?? raw.url ?? '').trim()
	const expiresAt = String(raw.expiresAt ?? raw.expires_at ?? '').trim()
	const status = String(raw.status ?? 'auth_required').trim() || 'auth_required'
	const message = String(raw.message ?? raw.description ?? '').trim()
	return { status, challengeId, ...(verificationUrl ? { verificationUrl } : {}), ...(expiresAt ? { expiresAt } : {}), ...(message ? { message } : {}) }
}

function challengeIsExpired(challenge: ProviderAuthChallenge | null, now = Date.now()): boolean {
	if (!challenge?.expiresAt) return false
	const timestamp = Date.parse(challenge.expiresAt)
	return Number.isFinite(timestamp) && timestamp <= now
}

function authChallengeStatus(provider: Provider, challenge: ProviderAuthChallenge | null): boolean {
	if (provider.type !== 'xiaomi-miot-cloud') return false
	const status = String(provider.status ?? '').toLowerCase()
	return Boolean(challenge?.challengeId) || ['auth_required', 'authentication_required', 'verification_required', 'challenge'].includes(status)
}

function providerTypeName(type: string): string {
	if (type === 'xiaomi') return '小米中枢网关'
	if (type === 'xiaomi-miot-cloud') return '小米 MIoT 云 · 第三方兼容'
	if (type === 'gree') return 'Gree 局域网空调'
	if (type === 'network') return '局域网设备监测 / Wake-on-LAN'
	if (type === 'tuya') return 'Tuya 涂鸦云'
	if (type === 'sonoff') return 'Sonoff/eWeLink · LAN 优先'
	if (type === 'mqtt') return 'MQTT · HomeLoom v1'
	if (type === 'virtual') return 'Virtual Runtime'
	return type
}

export function ProviderCard({ provider, devices, onEdit, onDelete, onRestart, onSimulate, onManageDevices, onDeviceLocation, onRevokeCredentials, onTest, onAuthChallengeComplete }: {
	provider: Provider
	devices: Device[]
	onEdit: (provider: Provider) => void
	onDelete: (provider: Provider) => void
	onRestart: (provider: Provider) => Promise<void>
	onRevokeCredentials?: (provider: Provider) => Promise<void>
	onSimulate: (device: Device, values: SimulationValues) => Promise<void>
	onManageDevices?: (provider: Provider) => void
	onDeviceLocation?: (device: Device) => void
	onTest?: (provider: ProviderInput) => Promise<void>
	onAuthChallengeComplete?: (provider: Provider) => Promise<void>
}) {
	const config = objectValue(provider.config)
	const oauth = objectValue(config.oauth)
	const configuredDevices = arrayValue(provider.type === 'camera' ? config.cameras : config.devices)
	const greeDevice = provider.type === 'gree' ? objectValue(configuredDevices[0] ?? config) : {}
	const greeHost = String(greeDevice.host ?? '')
	const greePort = Number(greeDevice.port || 7000)
	const greeMAC = String(greeDevice.mac ?? '')
	const greeName = String(greeDevice.name ?? '')
	const greePollInterval = Number(config.pollIntervalSeconds || 60)
	const greeRequestTimeout = Number(config.requestTimeoutSeconds || 5)
	const greeDeviceConfigured = provider.type === 'gree' && Boolean(greeHost && greeMAC && greeName)
	const networkProbeInterval = Number(config.probeIntervalSeconds || 30)
	const networkProbeTimeout = Number(config.probeTimeoutSeconds || 3)
	const tuyaAuthType = String(config.authType ?? '').trim().toLowerCase()
	const tuyaConfigured = provider.type === 'tuya' && Boolean(config.uid && (tuyaAuthType === 'sharing' || tuyaAuthType === 'homeassistant' ? config.endpoint && config.terminalId && config.accessToken && config.refreshToken : config.accessId && config.accessSecret))
	const sonoffCloud = objectValue(config.cloud)
	const sonoffConfigured = provider.type === 'sonoff' && Boolean(configuredDevices.length || sonoffCloud.accessToken || (sonoffCloud.username && sonoffCloud.password))
	const expectedDeviceCount = provider.type === 'virtual' && !Array.isArray(config.devices) ? devices.length : provider.type === 'tuya' ? devices.length : provider.type === 'gree' && configuredDevices.length === 0 && greeHost ? 1 : configuredDevices.length
	const features = Object.entries(provider.capabilities || {}).filter(([, enabled]) => enabled).map(([name]) => name)
	const onlineDevices = devices.filter((item) => item.availability === 'online').length
	const authorized = Boolean(oauth.clientId && oauth.oauthUuid && oauth.virtualDid)
	const certificateReady = Boolean(config.clientId && config.clientCertificate && config.privateKey)
	const mqttMode = String(config.mode ?? 'client') === 'server' ? 'server' : 'client'
	const displayedProviderType = provider.type === 'mqtt' ? `mqtt-${mqttMode}` : provider.type
	const brokerReady = mqttMode === 'server' ? Boolean(config.listenAddress) : Boolean(config.brokerUrl)
	const cloudSessionReady = Boolean((config.username && config.password) || (config.userId && config.ssecurity && config.serviceToken))
	const credentialsRevoked = Boolean(config.credentialsRevoked)
	const configuredAuthChallenge = normalizeAuthChallenge(provider.authChallenge)
	const [runtimeAuthChallenge, setRuntimeAuthChallenge] = useState<ProviderAuthChallenge | null>(configuredAuthChallenge)
	const [authChallengeCode, setAuthChallengeCode] = useState('')
	const [authChallengeBusy, setAuthChallengeBusy] = useState(false)
	const [authChallengeResult, setAuthChallengeResult] = useState<string | null>(null)
	const [authChallengeError, setAuthChallengeError] = useState<string | null>(null)
	const [authChallengeClock, setAuthChallengeClock] = useState(() => Date.now())
	const [authChallengeLoaded, setAuthChallengeLoaded] = useState(Boolean(configuredAuthChallenge))
	const authChallenge = runtimeAuthChallenge
	const authRequired = authChallengeStatus(provider, authChallenge)
	const authChallengeExpired = challengeIsExpired(authChallenge, authChallengeClock)
	const shouldFetchAuthChallenge = authChallengeStatus(provider, configuredAuthChallenge) && !configuredAuthChallenge
	const authChallengeUnavailable = authRequired && authChallengeLoaded && !authChallenge
	const providerID = provider.id
	const providerStatus = provider.status
	const configuredChallengeID = configuredAuthChallenge?.challengeId ?? configuredAuthChallenge?.challenge_id ?? ''
	const configuredChallengeURL = configuredAuthChallenge?.verificationUrl ?? configuredAuthChallenge?.verification_url ?? configuredAuthChallenge?.url ?? ''
	const configuredChallengeExpiry = configuredAuthChallenge?.expiresAt ?? configuredAuthChallenge?.expires_at ?? ''
	const configuredChallengeStatus = configuredAuthChallenge?.status ?? 'auth_required'
	const configuredChallengeMessage = configuredAuthChallenge?.message ?? ''
	useEffect(() => {
		setRuntimeAuthChallenge(configuredChallengeID ? { status: configuredChallengeStatus, challengeId: configuredChallengeID, ...(configuredChallengeURL ? { verificationUrl: configuredChallengeURL } : {}), ...(configuredChallengeExpiry ? { expiresAt: configuredChallengeExpiry } : {}), ...(configuredChallengeMessage ? { message: configuredChallengeMessage } : {}) } : null)
		setAuthChallengeLoaded(Boolean(configuredChallengeID))
		setAuthChallengeCode('')
		setAuthChallengeError(null)
		setAuthChallengeResult(null)
	}, [providerID, providerStatus, configuredChallengeID, configuredChallengeURL, configuredChallengeExpiry, configuredChallengeStatus, configuredChallengeMessage])
	useEffect(() => {
		if (!shouldFetchAuthChallenge) return
		let active = true
		setAuthChallengeLoaded(false)
		void getXiaomiProviderAuthChallenge(providerID).then((challenge) => {
			if (active) setRuntimeAuthChallenge(normalizeAuthChallenge(challenge))
			if (active) setAuthChallengeLoaded(true)
		}).catch(() => {
			if (active) setAuthChallengeLoaded(true)
			/* Provider.error remains the durable diagnostic when the challenge endpoint is unavailable. */
		})
		return () => { active = false }
	}, [providerID, providerStatus, shouldFetchAuthChallenge])
	useEffect(() => {
		if (!authChallenge?.expiresAt) return
		const expiresAt = Date.parse(authChallenge.expiresAt)
		if (!Number.isFinite(expiresAt)) return
		const delay = expiresAt - Date.now()
		if (delay <= 0) {
			setAuthChallengeClock(Date.now())
			return
		}
		const timer = window.setTimeout(() => setAuthChallengeClock(Date.now()), Math.min(delay + 1, 2_147_483_647))
		return () => window.clearTimeout(timer)
	}, [authChallenge?.expiresAt])
	const setupReady = !credentialsRevoked && (provider.type === 'camera' || provider.type === 'virtual' ? provider.enabled : provider.type === 'xiaomi' ? authorized : provider.type === 'xiaomi-miot-cloud' ? cloudSessionReady && !authRequired : provider.type === 'mqtt' ? brokerReady : provider.type === 'gree' ? greeDeviceConfigured : provider.type === 'tuya' ? tuyaConfigured : provider.type === 'sonoff' ? sonoffConfigured : configuredDevices.length > 0)
	const setupLabel = credentialsRevoked ? '凭据已注销' : provider.type === 'camera' ? `${configuredDevices.length} 台子设备` : provider.type === 'virtual' ? `${expectedDeviceCount} 台子设备` : provider.type === 'xiaomi' ? (authorized ? '已授权' : '待授权') : provider.type === 'xiaomi-miot-cloud' ? (authRequired ? '需短信验证' : cloudSessionReady ? '账号就绪' : '待登录') : provider.type === 'mqtt' ? (brokerReady ? (mqttMode === 'server' ? '监听就绪' : 'Broker 就绪') : '待配置') : provider.type === 'gree' ? (greeDeviceConfigured ? `${expectedDeviceCount} 台设备` : expectedDeviceCount ? `${expectedDeviceCount} 台（待补全）` : '待配置') : provider.type === 'network' ? (expectedDeviceCount ? `${expectedDeviceCount} 台待监测` : '待配置') : provider.type === 'tuya' ? (tuyaConfigured ? `${expectedDeviceCount} 台已发现` : '待配置') : provider.type === 'sonoff' ? (configuredDevices.length ? `${configuredDevices.length} 台已管理` : sonoffConfigured ? '账号就绪 · 待选设备' : '待登录') : `${configuredDevices.length} 台`
	const setupDetail = credentialsRevoked ? '本地 Token、会话与证书已清除；重新授权后才能启用。' : provider.type === 'camera' ? 'Provider 与摄像头连接配置分离' : provider.type === 'virtual' ? 'Provider 与虚拟设备配置分离' : provider.type === 'xiaomi' ? `${String(oauth.region || 'cn').toUpperCase()} · UID ${String(oauth.uid || '—')}` : provider.type === 'xiaomi-miot-cloud' ? `${String(config.region || 'cn').toUpperCase()} · ${String(config.username || config.userId || '未配置账号')}` : provider.type === 'mqtt' ? mqttMode === 'server' ? String(config.listenAddress || '尚未设置监听地址') : String(config.brokerUrl || '尚未设置 Broker') : provider.type === 'gree' ? `${greeName || '未命名 Gree 设备'} · ${greeHost || '未设置 host'}:${greePort} · MAC ${greeMAC || '未设置'}` : provider.type === 'network' ? `${expectedDeviceCount} 台设备 · TCP 电源状态探测${config.wolBroadcastAddress ? ' · Wake-on-LAN 已配置' : ''}` : provider.type === 'tuya' ? `${String(config.region || 'cn').toUpperCase()} · UID ${String(config.uid || '未配置')}` : '数据库期望设备'
	const connectionReady = provider.status === 'running'
	const connectionLabel = provider.type === 'xiaomi' ? (certificateReady ? '证书就绪' : '待申请证书') : provider.type === 'xiaomi-miot-cloud' ? (authRequired ? (authChallengeExpired ? '挑战已过期' : '需要短信验证') : connectionReady ? '云会话可用' : provider.status) : provider.type === 'mqtt' ? (connectionReady ? (mqttMode === 'server' ? '服务端监听中' : 'Broker 已连接') : provider.status) : connectionReady ? '已连接' : provider.status
	const connectionDetail = provider.type === 'camera' ? (connectionReady ? 'Media Worker / Camera Kernel 已按 Provider 启用' : 'Media Worker / Camera Kernel 未启用') : provider.type === 'xiaomi' ? `${String(config.host || '未选择中枢')}:${Number(config.port || 8883)} · MQTT 本地优先 / OAuth 官方云回退` : provider.type === 'xiaomi-miot-cloud' ? `轮询 ${Number(config.pollIntervalSeconds || 30)} 秒 · 非官方兼容接口` : provider.type === 'gree' ? `${greeHost || '未配置 host'}:${greePort} · 局域网轮询 ${greePollInterval} 秒 · 请求超时 ${greeRequestTimeout} 秒` : provider.type === 'network' ? `TCP 探测 ${networkProbeInterval} 秒 · 超时 ${networkProbeTimeout} 秒 · 连续失败 ${Number(config.offlineThreshold || 1)} 次后关闭` : provider.type === 'tuya' ? `轮询 ${Number(config.pollIntervalSeconds || 60)} 秒 · ${String(config.authType || '').toLowerCase() === 'sharing' || String(config.authType || '').toLowerCase() === 'homeassistant' ? 'Home Assistant 兼容扫码' : 'Tuya OpenAPI'}` : provider.type === 'mqtt' ? mqttMode === 'server' ? '外部设备主动连接 · 内嵌 Broker' : `${String(config.clientId || `homeloom-${provider.id}`)} · MQTT 客户端会话` : '进程内异步事件源'
	const cloudMqttLabel = provider.metrics?.cloudMqttConnected ? '已连接' : provider.metrics?.cloudMqttConfigured ? '重连中' : '未配置'
	const cloudMqttDetail = provider.metrics?.cloudMqttConnected
		? `最近连接 ${dateTime(provider.diagnostics?.cloudMqttLastConnectedAt)}`
		: provider.diagnostics?.cloudMqttLastError
			? `${provider.diagnostics.cloudMqttLastError}${provider.diagnostics.cloudMqttNextRetryAt ? ` · ${dateTime(provider.diagnostics.cloudMqttNextRetryAt)} 重试` : ''}`
			: provider.metrics?.cloudMqttConfigured ? '等待自动重连' : '完成 OAuth 后自动启用'
	const managedDeviceSource = supportsProviderChildDevices(provider.type)
	const [drafts, setDrafts] = useState<Record<string, string>>({})
	const [busy, setBusy] = useState<'test' | 'restart' | 'revoke' | null>(null)

	async function run(action: 'test' | 'restart' | 'revoke') {
		setBusy(action)
		try { if (action === 'test' && onTest) await onTest(provider); else if (action === 'restart') await onRestart(provider); else if (action === 'revoke' && onRevokeCredentials) await onRevokeCredentials(provider) } finally { setBusy(null) }
	}

	async function submitAuthChallenge() {
		const code = authChallengeCode.trim()
		if (!authChallenge?.challengeId || !code || authChallengeExpired) return
		setAuthChallengeBusy(true); setAuthChallengeError(null); setAuthChallengeResult(null)
		try {
			const updated = await verifyXiaomiProviderAuthChallenge(provider.id, { challengeId: authChallenge.challengeId, code })
			setRuntimeAuthChallenge(null); setAuthChallengeCode(''); setAuthChallengeResult('短信验证码验证成功，Provider 会话已更新。')
			if (onAuthChallengeComplete) await onAuthChallengeComplete(updated)
		} catch (cause) {
			setAuthChallengeCode('')
			const message = cause instanceof Error ? cause.message : '小米短信验证码校验失败'
			if (/challenge.*(?:missing|expired)|expired.*challenge|start login again|too many .*attempts/i.test(message)) {
				setRuntimeAuthChallenge(null)
				setAuthChallengeError('验证会话已过期或已失效，请重新打开 Provider 配置并重新登录。')
			} else setAuthChallengeError(message)
		} finally { setAuthChallengeBusy(false) }
	}

	const providerEditLabel = authChallengeUnavailable ? '重新登录小米云账号' : authRequired ? '继续短信验证' : provider.type === 'camera' || provider.type === 'virtual' ? 'Provider 配置' : provider.type === 'xiaomi' ? '账号与中枢' : provider.type === 'xiaomi-miot-cloud' ? '云账号配置' : provider.type === 'gree' ? '设备与连接' : provider.type === 'network' ? '监测与唤醒配置' : provider.type === 'tuya' ? 'Tuya 账号配置' : provider.type === 'sonoff' ? 'eWeLink 账号配置' : provider.type === 'mqtt' ? (mqttMode === 'server' ? '监听配置' : 'Broker 配置') : '配置'

	const numericControl = (item: Device, field: 'temperature' | 'humidity' | 'speed' | 'filterLife' | 'position', value: number) => {
		const key = `${item.id}:${field}`
		const label = { temperature: '温度', humidity: '湿度', speed: '速度', filterLife: '滤芯寿命', position: '位置' }[field]
		return <><input aria-label={`${item.name}${label}`} type="number" min={field === 'temperature' ? -100 : 0} max={field === 'temperature' ? 200 : 100} step={field === 'position' ? 1 : 0.1} value={drafts[key] ?? String(value)} onChange={(event) => setDrafts((current) => ({ ...current, [key]: event.target.value }))} /><button onClick={() => { const next = Number(drafts[key] ?? value); void onSimulate(item, { [field]: next } as SimulationValues) }}>上报</button></>
	}

	return <article className="provider-card provider-runtime-card">
		<header>
			<div><div className="device-card__topline"><span className={`status-dot ${connectionReady ? 'is-online' : ''}`} />{provider.status}<span className="provider">{displayedProviderType}</span></div><h3>{provider.name}</h3><p>{provider.type === 'mqtt' ? `MQTT ${mqttMode === 'server' ? '服务端（SERVER）' : '客户端（CLIENT）'} · HomeLoom v1` : providerTypeName(provider.type)} · {provider.id}</p></div>
			<div className="provider-card__actions"><button onClick={() => onEdit(provider)}>{providerEditLabel}</button>{managedDeviceSource && onManageDevices && <button disabled={!connectionReady} title={connectionReady ? '使用当前运行连接配置设备' : '请先连接设备来源'} onClick={() => onManageDevices(provider)}>{provider.type === 'camera' ? '管理摄像头' : provider.type === 'virtual' ? '管理虚拟设备' : provider.type === 'gree' ? '管理格力设备' : provider.type === 'network' ? '管理网络设备' : '管理设备'}</button>}{onTest && <button disabled={busy !== null || (provider.type === 'xiaomi' && !certificateReady)} onClick={() => void run('test')}>{busy === 'test' ? '测试中…' : provider.type === 'mqtt' && mqttMode === 'server' ? '测试监听' : '测试连接'}</button>}{provider.enabled && <button disabled={busy !== null} onClick={() => void run('restart')}>{busy === 'restart' ? (provider.type === 'mqtt' && mqttMode === 'server' ? '重启监听中…' : '重连中…') : provider.type === 'mqtt' && mqttMode === 'server' ? '重启监听' : '重新连接'}</button>}{(provider.type === 'xiaomi' || provider.type === 'xiaomi-miot-cloud') && onRevokeCredentials && !credentialsRevoked && <button className="is-danger" disabled={busy !== null} onClick={() => void run('revoke')}>{busy === 'revoke' ? '注销中…' : '注销凭据'}</button>}<button className="is-danger" onClick={() => onDelete(provider)}>删除</button></div>
		</header>
		<div className="provider-status-grid">
			<div><span>{provider.type === 'xiaomi' || provider.type === 'xiaomi-miot-cloud' || provider.type === 'tuya' ? '账号与凭据' : provider.type === 'mqtt' ? (mqttMode === 'server' ? '服务端配置' : 'Broker 配置') : '设备配置'}</span><strong className={setupReady ? 'is-ready' : ''}>{setupLabel}</strong><small>{setupDetail}</small></div>
			<div><span>运行连接</span><strong className={connectionReady ? 'is-ready' : ''}>{connectionLabel}</strong><small>{connectionDetail}</small></div>
			{provider.type === 'xiaomi' && <div><span>官方云 MQTT</span><strong className={provider.metrics?.cloudMqttConnected ? 'is-ready' : ''}>{cloudMqttLabel}</strong><small>{cloudMqttDetail}</small></div>}
			<div><span>期望设备</span><strong>{expectedDeviceCount}</strong><small>{provider.type === 'mqtt' ? '数据库设备路由 · 严格白名单' : provider.type === 'gree' ? '格力设备管理器配置' : provider.type === 'network' ? '网络设备监测配置' : '数据库配置'}</small></div>
			<div><span>实时发布</span><strong>{onlineDevices} / {devices.length}</strong><small>当前内存快照</small></div>
		</div>
		<div className="provider-runtime"><span>能力 <b>{features.length}</b></span>{provider.type === 'mqtt' ? <><span>消息 <b>{provider.metrics?.messagesReceived ?? 0}</b></span><span>无效 <b>{provider.metrics?.messagesInvalid ?? 0}</b></span><span>丢弃 <b>{provider.metrics?.messagesDropped ?? 0}</b></span><span>命令 <b>{provider.metrics?.commandsPublished ?? 0}</b></span></> : <><span title="Provider 本次运行以来发起的累计请求">累计请求 <b>{provider.metrics?.requests ?? 0}</b></span><span>状态事件 <b>{provider.metrics?.events ?? 0}</b></span><span title="本地与云端回退后仍未成功的最终错误">最终失败 <b>{provider.metrics?.errors ?? 0}</b></span>{provider.type === 'xiaomi' && <><span title="向中枢本地控制通道发起的累计请求">本地请求 <b>{provider.metrics?.localRequests ?? 0}</b></span><span title="本地请求未完成；其中一部分可能已由官方云成功接管">本地失败 <b>{provider.metrics?.localFailures ?? 0}</b></span><span title="auto 模式从本地路径切换到官方云 HTTP 的累计次数">自动转云 <b>{provider.metrics?.cloudFallbacks ?? 0}</b></span><span title="设备目录、初始读取、状态校准及控制产生的累计官方云 HTTP 请求">云 HTTP 请求 <b>{provider.metrics?.cloudRequests ?? 0}</b></span><span>官方云 MQTT <b>{cloudMqttLabel}</b></span><span title="官方云 MQTT 收到的累计推送消息">云 MQTT 推送 <b>{provider.metrics?.cloudMqttMessagesReceived ?? 0}</b></span></>}</>}</div>
		{provider.credentials?.managed && <div className="provider-credential-status"><div><strong>凭据自动续期</strong><small>下次检查 {dateTime(provider.credentials.refreshAt)}</small></div><span>Token 到期 <b>{dateTime(provider.credentials.tokenExpiresAt)}</b></span><span>证书到期 <b>{dateTime(provider.credentials.certificateExpiresAt)}</b></span>{provider.credentialError && <p role="alert">续期失败：{provider.credentialError} · {dateTime(provider.credentialRetryAt)} 重试</p>}</div>}
		{authRequired && <div className="provider-auth-challenge" role="region" aria-label="小米短信验证"><div><strong>{authChallengeExpired ? '小米短信验证已过期' : '小米账号需要短信验证'}</strong><small>{authChallengeExpired ? '请重新打开 Provider 配置并登录，以获取新的验证会话。' : authChallenge?.message || 'Provider 启动需要完成身份验证；请打开验证页发送验证码，再回到这里提交。'}</small></div>{authChallenge?.verificationUrl && <a href={authChallenge.verificationUrl} target="_blank" rel="noreferrer">打开小米身份验证页面</a>}{authChallengeExpired ? <p role="alert" className="inline-error">验证会话已过期，请重新登录后再次获取验证码。</p> : authChallenge?.challengeId ? <div className="provider-auth-challenge__input"><label>短信验证码<input aria-label="Provider 小米短信验证码" inputMode="numeric" autoComplete="one-time-code" value={authChallengeCode} onChange={(event) => setAuthChallengeCode(event.target.value)} /></label><button type="button" disabled={authChallengeBusy || !authChallengeCode.trim()} onClick={() => void submitAuthChallenge()}>{authChallengeBusy ? '正在验证…' : '提交验证码并继续'}</button></div> : authChallengeUnavailable ? <div role="alert" className="inline-error"><p>此前的短信验证会话已经失效，当前验证码无法提交。</p><p>请重新填写真实账号密码并发起登录；新的验证会话创建后，验证码输入框会在配置页显示。</p><button type="button" onClick={() => onEdit(provider)}>打开配置并重新登录</button></div> : <p>正在读取验证会话…</p>}{authChallengeResult && <p className="test-success" role="status">{authChallengeResult}</p>}{authChallengeError && <p role="alert" className="inline-error">{authChallengeError}</p>}{authChallenge?.expiresAt && <small>挑战截止 {dateTime(authChallenge.expiresAt)}</small>}</div>}
		{provider.error && <div className="provider-error"><p className="inline-error">{provider.error}</p><small>已自动重试 {provider.retryCount ?? 0} 次{provider.nextRetryAt ? ` · 下次 ${new Date(provider.nextRetryAt).toLocaleTimeString()}` : ''}</small></div>}
		<div className="provider-device-list"><div className="command-heading"><h3>已发布设备</h3><span>{devices.length} 台</span></div>{devices.length === 0 ? <p>实例连接并完成发现或设备配置后，设备会在这里显示。</p> : devices.map((item) => <div key={item.id}><span className={`status-dot is-${item.availability}`} /><div className="provider-device-name"><strong>{item.name}</strong>{onDeviceLocation && <button type="button" onClick={() => onDeviceLocation(item)}>{item.locationMode === 'custom' ? `${item.homeName}${item.roomName ? ` / ${item.roomName}` : ''}` : '设置位置'}</button>}</div><code>{item.id}</code><small className="provider-device-runtime"><span>{item.type === 'network-device' ? '网络设备 · 电源状态' : item.type} · seq {item.sequence ?? '—'}</span>{(provider.type === 'xiaomi' || provider.type === 'xiaomi-miot-cloud') && item.runtimeMode && <i className={`device-runtime-mode is-${item.runtimeMode}`}>{runtimeModeLabel(item.runtimeMode, item.stateTransport)}</i>}</small></div>)}</div>
		{provider.type === 'virtual' && devices.length > 0 && <div className="simulation-panel"><div><strong>运行时模拟</strong><small>状态只保存在内存中，重启后按配置重建</small></div>{devices.map((item) => {
			const powered = item.type === 'switch' || item.type === 'lightbulb' || item.type === 'outlet'
			const power = propertyBool(item, 'switch', 'power'); const contact = propertyBool(item, 'contact', 'contact-detected'); const motion = propertyBool(item, 'motion', 'motion-detected'); const advancedCapability = item.type === 'fan' ? 'fan' : 'air-purifier'; const active = propertyBool(item, advancedCapability, 'active'); const mode = propertyString(item, advancedCapability, 'target-state'); const filterChange = propertyBool(item, 'filter', 'change-indication')
			return <div className="simulation-device" key={item.id}><div className="simulation-name"><span className={`status-dot is-${item.availability}`} /><b>{item.name}</b><small>{item.id} · {availabilityLabel(item.availability)} · seq {item.sequence ?? '—'}</small></div><div className="simulation-actions"><button onClick={() => void onSimulate(item, { online: !item.online })}>{item.online ? '设为离线' : '恢复在线'}</button><button onClick={() => void onSimulate(item, { availability: 'unknown' })}>设为未知</button><button onClick={() => void onSimulate(item, { repeat: 2 })}>重复事件</button><button onClick={() => void onSimulate(item, { sequence: Math.max(1, (item.sequence ?? 1) - 1) })}>旧序列事件</button>{powered && <button onClick={() => void onSimulate(item, { power: !power })}>{power ? '关闭' : '打开'}</button>}{item.type === 'temperature-sensor' && numericControl(item, 'temperature', propertyNumber(item, 'temperature', 'current-temperature'))}{item.type === 'humidity-sensor' && numericControl(item, 'humidity', propertyNumber(item, 'humidity', 'current-humidity'))}{item.type === 'temperature-humidity-sensor' && <>{numericControl(item, 'temperature', propertyNumber(item, 'temperature', 'current-temperature'))}{numericControl(item, 'humidity', propertyNumber(item, 'humidity', 'current-humidity'))}</>}{item.type === 'contact-sensor' && <button onClick={() => void onSimulate(item, { contact: !contact })}>{contact ? '设为打开' : '设为闭合'}</button>}{item.type === 'motion-sensor' && <button onClick={() => void onSimulate(item, { motion: !motion })}>{motion ? '清除活动' : '触发活动'}</button>}{(item.type === 'fan' || item.type === 'air-purifier') && <><button onClick={() => void onSimulate(item, { active: !active })}>{active ? '停止' : '启动'}</button><button onClick={() => void onSimulate(item, { mode: mode === 'auto' ? 'manual' : 'auto' })}>{mode === 'auto' ? '手动模式' : '自动模式'}</button>{numericControl(item, 'speed', propertyNumber(item, advancedCapability, 'rotation-speed'))}</>}{item.type === 'air-purifier' && <>{numericControl(item, 'filterLife', propertyNumber(item, 'filter', 'life-level'))}<button onClick={() => void onSimulate(item, { filterChange: !filterChange })}>{filterChange ? '标记滤芯正常' : '标记需换滤芯'}</button></>}{item.type === 'window-covering' && numericControl(item, 'position', propertyInt(item, 'window-covering', 'current-position'))}</div></div>
		})}</div>}
	</article>
}
