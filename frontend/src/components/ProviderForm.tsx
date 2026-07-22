import { useState } from 'react'
import type { Provider, ProviderInput } from '../types/provider'
import { ApiError } from '../api/client'
import { completeXiaomiOAuth, discoverXiaomiGateways, startXiaomiCloudLogin, startXiaomiOAuth, verifyXiaomiCloudLogin, type XiaomiCloudLoginResult, type XiaomiGateway } from '../api/xiaomi'

const xiaomiOAuthRedirectURL = 'http://homeassistant.local:8123'
const expandedVirtualExamples = [
	['illuminance', '客厅照度', 'illuminance-sensor'], ['occupancy', '书房占用', 'occupancy-sensor'],
	['leak', '厨房漏水', 'leak-sensor'], ['smoke', '客厅烟雾', 'smoke-sensor'],
	['carbon-monoxide', '一氧化碳监测', 'carbon-monoxide-sensor'], ['carbon-dioxide', '二氧化碳监测', 'carbon-dioxide-sensor'],
	['air-quality', '全屋空气质量', 'air-quality-sensor'], ['thermostat', '客厅恒温器', 'thermostat'],
	['air-conditioner', '客厅空调', 'air-conditioner'], ['heater-cooler', '卧室冷暖风机', 'heater-cooler'], ['humidifier', '书房加湿器', 'humidifier-dehumidifier'],
	['lock', '入户门锁', 'lock'], ['garage-door', '车库门', 'garage-door'],
	['security-system', '家庭安防', 'security-system'], ['valve', '花园水阀', 'valve'],
	['speaker', '客厅扬声器', 'speaker'], ['robot-vacuum', '扫地机器人', 'robot-vacuum'],
].map(([id, name, type]) => ({ id: `demo-${id}`, name, type, online: true }))

function createXiaomiExample() {
	return { host: '', port: 8883, clientId: '', caCertificate: '', clientCertificate: '', privateKey: '', serverName: '', insecureSkipVerify: false, requestTimeoutSeconds: 10, pollIntervalSeconds: 60, oauth: { clientId: '', region: 'cn', redirectUrl: xiaomiOAuthRedirectURL, oauthUuid: '', virtualDid: '' }, devices: [] }
}

function createXiaomiMIoTCloudExample() {
	return { region: 'cn', username: '', password: '', pollIntervalSeconds: 30, requestTimeoutSeconds: 15, devices: [] }
}

function createMQTTExample(mode: 'client' | 'server' = 'client') {
	if (mode === 'server') return { mode, listenAddress: '127.0.0.1:1883', username: '', password: '', connectTimeoutSeconds: 10, retainedStateMaxAgeSeconds: 300, tls: {}, devices: [] }
	return { mode, brokerUrl: 'mqtt://127.0.0.1:1883', username: '', password: '', clientId: '', keepAliveSeconds: 30, connectTimeoutSeconds: 10, sessionExpirySeconds: 86400, retainedStateMaxAgeSeconds: 300, tls: {}, devices: [] }
}

type ProviderSelection = 'virtual' | 'mqtt-client' | 'mqtt-server' | 'xiaomi' | 'xiaomi-miot-cloud'

export function ProviderForm({ provider, initialType, onCancel, onSave, onTest }: { provider: Provider | null; initialType?: ProviderSelection | 'mqtt'; onCancel: () => void; onSave: (input: ProviderInput, editing: boolean) => Promise<void>; onTest?: (input: ProviderInput) => Promise<void> }) {
	const selectedInitialType: ProviderSelection = provider?.type === 'mqtt' ? (String(provider.config.mode ?? 'client') === 'server' ? 'mqtt-server' : 'mqtt-client') : initialType === 'mqtt' ? 'mqtt-client' : (provider?.type ?? initialType ?? 'virtual') as ProviderSelection
	const initialXiaomiConfig = createXiaomiExample()
	const initialXiaomiCloudConfig = createXiaomiMIoTCloudExample()
	const initialMQTTConfig = createMQTTExample(selectedInitialType === 'mqtt-server' ? 'server' : 'client')
  const [id, setID] = useState(provider?.id ?? '')
  const [name, setName] = useState(provider?.name ?? '')
  const [type, setType] = useState(selectedInitialType)
  const [enabled, setEnabled] = useState(provider?.enabled ?? true)
	  const [config, setConfig] = useState(JSON.stringify(provider?.config ?? (selectedInitialType === 'mqtt-client' || selectedInitialType === 'mqtt-server' ? initialMQTTConfig : selectedInitialType === 'xiaomi' ? initialXiaomiConfig : selectedInitialType === 'xiaomi-miot-cloud' ? initialXiaomiCloudConfig : {}), null, 2))
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
	const [cloudChallenge, setCloudChallenge] = useState<XiaomiCloudLoginResult | null>(null)
	const [cloudVerificationCode, setCloudVerificationCode] = useState('')
	const [cloudAuthenticating, setCloudAuthenticating] = useState(false)
	const hasRedactedSecrets = Boolean(provider && JSON.stringify(provider.config).includes('********'))
  const example = { latencyMs: 0, rejectWrites: false, devices: [{ id: 'demo-switch', name: '客厅开关', type: 'switch', online: true, power: false }, { id: 'demo-light', name: '客厅灯', type: 'lightbulb', online: true, power: true, brightness: 80, colorTemperature: 250, hue: 35, saturation: 45 }, { id: 'demo-outlet', name: '书房插座', type: 'outlet', online: true, power: false, inUse: false, currentPower: 0, energy: 1.25 }, { id: 'demo-sensor', name: '客厅温度', type: 'single-property-sensor', online: true, value: 23.6, unit: 'celsius', batteryLevel: 91, lowBattery: false }, { id: 'demo-climate', name: '客厅温湿度', type: 'temperature-humidity-sensor', online: true, temperature: 23.6, humidity: 56.2, batteryLevel: 87, lowBattery: false }, { id: 'demo-contact', name: '入户门', type: 'contact-sensor', online: true, contact: false, batteryLevel: 88, lowBattery: false, tampered: false }, { id: 'demo-motion', name: '走廊活动', type: 'motion-sensor', online: true, motion: false, batteryLevel: 84, lowBattery: false, tampered: false }, { id: 'demo-fan', name: '卧室风扇', type: 'fan', online: true, active: false, speed: 35, mode: 'manual', swingMode: true, direction: 'clockwise', controlLock: false }, { id: 'demo-air', name: '客厅净化器', type: 'air-purifier', online: true, active: true, speed: 60, mode: 'auto', swingMode: false, controlLock: false, airQuality: 'good', pm25: 12, voc: 80, filterLife: 82, filterChange: false }, { id: 'demo-shade', name: '南窗帘', type: 'window-covering', online: true, position: 50, obstruction: false }, ...expandedVirtualExamples] }
		const xiaomiExample = initialXiaomiConfig
		const xiaomiCloudExample = initialXiaomiCloudConfig
	let configObject: Record<string, unknown> = {}
	try { const parsed = JSON.parse(config) as unknown; if (parsed && !Array.isArray(parsed) && typeof parsed === 'object') configObject = parsed as Record<string, unknown> } catch { /* validation is shown on submit */ }
	const tlsConfig = configObject.tls && !Array.isArray(configObject.tls) && typeof configObject.tls === 'object' ? configObject.tls as Record<string, unknown> : {}
	const xiaomiOAuth = configObject.oauth && !Array.isArray(configObject.oauth) && typeof configObject.oauth === 'object' ? configObject.oauth as Record<string, unknown> : {}
	const mqttSelected = type === 'mqtt-client' || type === 'mqtt-server'
	const mqttServer = type === 'mqtt-server'
	const providerType = mqttSelected ? 'mqtt' : type
	const updateMQTT = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, [key]: value }, null, 2))
	const updateTLS = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, tls: { ...tlsConfig, [key]: value } }, null, 2))
	const updateXiaomi = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, [key]: value }, null, 2))
	const updateXiaomiOAuth = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, oauth: { ...xiaomiOAuth, [key]: value } }, null, 2))
	const updateXiaomiCloudIdentity = (key: string, value: unknown) => { updateXiaomi(key, value); setCloudChallenge(null); setCloudVerificationCode('') }
	const cloudSessionReady = ['userId', 'ssecurity', 'serviceToken'].every((key) => typeof configObject[key] === 'string' && String(configObject[key]).length > 0)
	function applyCloudSession(result: XiaomiCloudLoginResult) {
		if (result.status !== 'verified' || !result.userId || !result.ssecurity || !result.serviceToken) throw new Error('小米云登录未返回完整会话，请重新登录')
		setConfig(JSON.stringify({ ...configObject, userId: result.userId, ssecurity: result.ssecurity, serviceToken: result.serviceToken }, null, 2))
		setCloudChallenge(null); setCloudVerificationCode('')
		setTestResult('小米云账号验证成功，会话凭据已就绪；现在可以保存并启用 Provider。')
	}
	async function beginCloudLogin() {
		const username = String(configObject.username ?? '').trim()
		const password = String(configObject.password ?? '')
		if (!username || !password || password === '********') { setError('请输入当前的小米账号和真实密码后再登录'); return }
		setCloudAuthenticating(true); setError(null); setTestResult(null); setCloudChallenge(null); setCloudVerificationCode('')
		try {
			const result = await startXiaomiCloudLogin({ region: String(configObject.region ?? 'cn'), username, password, requestTimeoutSeconds: Number(configObject.requestTimeoutSeconds ?? 15) })
			if (result.status === 'verification_required') {
				if (!result.challengeId || !result.verificationUrl) throw new Error('小米要求身份验证，但没有返回验证入口')
				setCloudChallenge(result)
				setTestResult('小米要求身份验证。请打开验证页面发送短信或邮件验证码，然后回到这里填写。')
			} else applyCloudSession(result)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '小米云登录失败') } finally { setCloudAuthenticating(false) }
	}
	async function completeCloudVerification() {
		if (!cloudChallenge?.challengeId || !cloudVerificationCode.trim()) return
		setCloudAuthenticating(true); setError(null)
		try { applyCloudSession(await verifyXiaomiCloudLogin({ challengeId: cloudChallenge.challengeId, code: cloudVerificationCode.trim() })) }
		catch (cause) { setError(cause instanceof Error ? cause.message : '小米验证码校验失败') }
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
		if (type === 'xiaomi-miot-cloud' && !['userId', 'ssecurity', 'serviceToken'].every((key) => typeof parsed[key] === 'string' && String(parsed[key]).length > 0)) { setError('请先完成“小米云账号登录”；如触发短信或邮件验证，请回填验证码后再保存'); return }
		setSaving(true); setError(null); setFieldErrors({}); try { await onSave({ id, name, type: providerType, enabled, config: parsed }, Boolean(provider)) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败'); if (cause instanceof ApiError) setFieldErrors(cause.fields) } finally { setSaving(false) }
  }
	async function testConnection() {
		let parsed: Record<string, unknown>
		try { parsed = JSON.parse(config) as Record<string, unknown>; if (!parsed || Array.isArray(parsed)) throw new Error() } catch { setError('扩展配置必须是 JSON 对象'); return }
		if (mqttSelected) parsed.mode = mqttServer ? 'server' : 'client'
		if (!onTest) return
		setTesting(true); setError(null); setTestResult(null)
			try { await onTest({ id, name, type: providerType, enabled, config: parsed }); setTestResult(type === 'xiaomi-miot-cloud' ? '云账号登录成功，设备目录与 MIoT 属性接口可用。' : mqttSelected ? mqttServer ? 'MQTT 服务端监听测试成功。保存 Provider 后外部设备可连接此地址。' : 'MQTT 客户端已连接外部 Broker。保存 Provider 后再配置设备 Topic。' : '连接成功，订阅已建立。') } catch (cause) { setError(cause instanceof Error ? cause.message : '连接测试失败') } finally { setTesting(false) }
	}
	return <div className="modal-backdrop"><form className="target-form" role="dialog" aria-modal="true" aria-labelledby="provider-form-title" onSubmit={(event) => void submit(event)}>
		<div className="form-heading"><div><p className="eyebrow">PROVIDER</p><h2 id="provider-form-title">{provider ? '编辑 Provider' : '新建 Provider'}</h2></div><button type="button" onClick={onCancel}>关闭</button></div>
		    <div className="form-grid"><label>ID（留空自动生成）<input aria-invalid={Boolean(fieldErrors.id)} value={id} disabled={Boolean(provider)} onChange={(event) => setID(event.target.value)} placeholder={mqttSelected ? (mqttServer ? 'mqtt-server-main' : 'mqtt-client-main') : type === 'xiaomi' ? 'xiaomi-main' : type === 'xiaomi-miot-cloud' ? 'xiaomi-miot-cloud-main' : 'virtual-lab'} />{fieldErrors.id && <small className="field-error">{fieldErrors.id}</small>}</label><label>名称<input aria-invalid={Boolean(fieldErrors.name)} value={name} onChange={(event) => setName(event.target.value)} placeholder={mqttSelected ? (mqttServer ? '家庭 MQTT 服务端' : '家庭 MQTT 客户端') : type === 'xiaomi' ? '米家中枢网关' : type === 'xiaomi-miot-cloud' ? '小米 MIoT 云（第三方兼容）' : '实验室虚拟设备'} />{fieldErrors.name && <small className="field-error">{fieldErrors.name}</small>}</label><label className="wide">类型<select aria-invalid={Boolean(fieldErrors.type)} value={type} disabled={Boolean(provider)} onChange={(event) => { const next = event.target.value as ProviderSelection; setType(next); if (!provider) { const selected = next === 'mqtt-client' ? createMQTTExample('client') : next === 'mqtt-server' ? createMQTTExample('server') : next === 'xiaomi' ? xiaomiExample : next === 'xiaomi-miot-cloud' ? xiaomiCloudExample : example; setConfig(JSON.stringify(selected, null, 2)) } }}><option value="virtual">Virtual</option><option value="mqtt-client">MQTT Client（客户端 · 连接外部 Broker）</option><option value="mqtt-server">MQTT Server（服务端 · 接受设备连接）</option><option value="xiaomi">Xiaomi Central Hub（中枢网关）</option><option value="xiaomi-miot-cloud">Xiaomi MIoT Cloud（第三方兼容）</option></select>{fieldErrors.type && <small className="field-error">{fieldErrors.type}</small>}</label>
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
		</div> : type === 'xiaomi' ? <div className="wide xiaomi-connection-flow">
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
				{gateways.length > 0 && <div className="wide"><small>选择支持 MQTT 的主中枢：</small><div className="simulation-actions">{gateways.map((gateway) => <button type="button" key={`${gateway.instance}-${gateway.hostName}`} disabled={!gateway.mqttEnabled} onClick={() => setConfig(JSON.stringify({ ...configObject, host: gateway.addresses[0] ?? gateway.hostName, port: gateway.port }, null, 2))}>{gateway.instance} · {gateway.addresses[0] ?? gateway.hostName} · role {gateway.role ?? '—'}</button>)}</div></div>}
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
					{cloudSessionReady && <small className="test-success">云会话已就绪。保存后 Provider 将直接复用此会话，不会重复登录。</small>}
					{cloudChallenge?.verificationUrl && <div className="wide xiaomi-oauth-callback"><strong>需要短信或邮件验证</strong><ol><li><a href={cloudChallenge.verificationUrl} target="_blank" rel="noreferrer">打开小米身份验证页面</a>。</li><li>在小米页面选择手机号或邮箱并发送验证码；收到后不要在小米页面提交。</li><li>回到 HomeLoom，在下方填写验证码并继续登录。</li></ol><label>短信 / 邮件验证码<input aria-label="小米 MIoT 云验证码" inputMode="numeric" autoComplete="one-time-code" value={cloudVerificationCode} onChange={(event) => setCloudVerificationCode(event.target.value)} /></label><button type="button" disabled={cloudAuthenticating || !cloudVerificationCode.trim()} onClick={() => void completeCloudVerification()}>{cloudAuthenticating ? '正在验证…' : '提交验证码并继续登录'}</button>{cloudChallenge.expiresAt && <small>此登录会话将在 {new Date(cloudChallenge.expiresAt).toLocaleTimeString()} 过期；过期后请重新登录。</small>}</div>}
					{onTest && cloudSessionReady && <button type="button" className="example-button" disabled={testing || saving || cloudAuthenticating} onClick={() => void testConnection()}>{testing ? '正在读取…' : '测试 MIoT 云连接'}</button>}
				</div></section>
				<div className="xiaomi-next-step"><strong>02 · 保存并选择云端设备</strong><p>登录完成后保存并启用 Provider，再从 Provider 卡片进入“管理设备”；系统复用当前云会话读取账号设备目录，不会重复登录。</p></div>
				<details><summary>已有会话凭据（高级替代方案）</summary><div className="mqtt-tls-grid"><label>User ID（userId）<input aria-label="小米 MIoT 云 User ID" value={String(configObject.userId ?? '')} onChange={(event) => updateXiaomi('userId', event.target.value)} /></label><label>ssecurity<input aria-label="小米 MIoT 云 ssecurity" type="password" value={String(configObject.ssecurity ?? '')} onChange={(event) => updateXiaomi('ssecurity', event.target.value)} /></label><label>Service Token（serviceToken）<input aria-label="小米 MIoT 云 Service Token" type="password" value={String(configObject.serviceToken ?? '')} onChange={(event) => updateXiaomi('serviceToken', event.target.value)} /></label><small>三项必须同时填写，可替代账号密码；会话过期后若未保存账号密码，需要手动更新。</small></div></details>
				<small>密码与云会话 Token 保存到 PostgreSQL 并加密。设备局域网 Token 仅在后端云目录运行时使用，不通过管理 API 返回；没有本地能力的设备继续使用云轮询。该接口不适合无线开关、人体传感器等瞬时事件设备。</small>
				{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{testResult && <small className="test-success">{testResult}</small>}
			</div> : <label className="wide config-editor"><span>扩展配置（JSON）</span><textarea aria-invalid={Boolean(fieldErrors.config)} rows={11} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{hasRedactedSecrets && <small>敏感字段已显示为 ********；保持占位符即可沿用数据库中的原值，输入新值可进行替换。</small>}<small>支持 27 种内置统一模型及必须/可选参数；未进入标准契约的属性会标记为自定义参数。</small><button type="button" className="example-button" onClick={() => setConfig(JSON.stringify(example, null, 2))}>载入完整模型示例</button></label>}</div>
    <label className="enable-row"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />立即启用（无需重启服务）</label>
    {error && <p className="inline-error">{error}</p>}<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '应用中…' : '保存并应用'}</button></div>
  </form></div>
}
