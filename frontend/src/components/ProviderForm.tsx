import { useState } from 'react'
import type { Provider, ProviderInput } from '../types/provider'
import { ApiError } from '../api/client'
import { completeXiaomiOAuth, discoverXiaomiGateways, startXiaomiOAuth, type XiaomiGateway } from '../api/xiaomi'

const xiaomiOAuthRedirectURL = 'http://homeassistant.local:8123'

function createXiaomiExample() {
	return { host: '', port: 8883, clientId: '', caCertificate: '', clientCertificate: '', privateKey: '', serverName: '', insecureSkipVerify: false, requestTimeoutSeconds: 10, pollIntervalSeconds: 60, oauth: { clientId: '', region: 'cn', redirectUrl: xiaomiOAuthRedirectURL, oauthUuid: '', virtualDid: '' }, devices: [] }
}

export function ProviderForm({ provider, initialType, onCancel, onSave, onTest }: { provider: Provider | null; initialType?: 'virtual' | 'mqtt' | 'xiaomi'; onCancel: () => void; onSave: (input: ProviderInput, editing: boolean) => Promise<void>; onTest?: (input: ProviderInput) => Promise<void> }) {
	const selectedInitialType = provider?.type ?? initialType ?? 'virtual'
	const initialXiaomiConfig = createXiaomiExample()
  const [id, setID] = useState(provider?.id ?? '')
  const [name, setName] = useState(provider?.name ?? '')
  const [type, setType] = useState(selectedInitialType)
  const [enabled, setEnabled] = useState(provider?.enabled ?? true)
  const [config, setConfig] = useState(JSON.stringify(provider?.config ?? (selectedInitialType === 'xiaomi' ? initialXiaomiConfig : {}), null, 2))
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
	const hasRedactedSecrets = Boolean(provider && JSON.stringify(provider.config).includes('********'))
  const example = { latencyMs: 0, rejectWrites: false, devices: [{ id: 'demo-switch', name: '客厅开关', type: 'switch', online: true, power: false }, { id: 'demo-light', name: '客厅灯', type: 'lightbulb', online: true, power: true, brightness: 80, colorTemperature: 250, hue: 35, saturation: 45 }, { id: 'demo-outlet', name: '书房插座', type: 'outlet', online: true, power: false, inUse: false, currentPower: 0, energy: 1.25 }, { id: 'demo-sensor', name: '客厅温度', type: 'single-property-sensor', online: true, value: 23.6, unit: 'celsius', batteryLevel: 91, lowBattery: false }, { id: 'demo-climate', name: '客厅温湿度', type: 'temperature-humidity-sensor', online: true, temperature: 23.6, humidity: 56.2, batteryLevel: 87, lowBattery: false }, { id: 'demo-contact', name: '入户门', type: 'contact-sensor', online: true, contact: false, batteryLevel: 88, lowBattery: false, tampered: false }, { id: 'demo-motion', name: '走廊活动', type: 'motion-sensor', online: true, motion: false, batteryLevel: 84, lowBattery: false, tampered: false }, { id: 'demo-fan', name: '卧室风扇', type: 'fan', online: true, active: false, speed: 35, mode: 'manual', swingMode: true, direction: 'clockwise', controlLock: false }, { id: 'demo-air', name: '客厅净化器', type: 'air-purifier', online: true, active: true, speed: 60, mode: 'auto', swingMode: false, controlLock: false, airQuality: 'good', pm25: 12, voc: 80, filterLife: 82, filterChange: false }, { id: 'demo-shade', name: '南窗帘', type: 'window-covering', online: true, position: 50, obstruction: false }] }
	const mqttExample = { brokerUrl: 'mqtt://127.0.0.1:1883', username: '', password: '', clientId: '', topicPrefix: 'homeloom', qos: 1, keepAliveSeconds: 30, connectTimeoutSeconds: 10, sessionExpirySeconds: 86400, retainedStateMaxAgeSeconds: 300, tls: {} }
	const xiaomiExample = initialXiaomiConfig
	let configObject: Record<string, unknown> = {}
	try { const parsed = JSON.parse(config) as unknown; if (parsed && !Array.isArray(parsed) && typeof parsed === 'object') configObject = parsed as Record<string, unknown> } catch { /* validation is shown on submit */ }
	const tlsConfig = configObject.tls && !Array.isArray(configObject.tls) && typeof configObject.tls === 'object' ? configObject.tls as Record<string, unknown> : {}
	const xiaomiOAuth = configObject.oauth && !Array.isArray(configObject.oauth) && typeof configObject.oauth === 'object' ? configObject.oauth as Record<string, unknown> : {}
	const updateMQTT = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, [key]: value }, null, 2))
	const updateTLS = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, tls: { ...tlsConfig, [key]: value } }, null, 2))
	const updateXiaomi = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, [key]: value }, null, 2))
	const updateXiaomiOAuth = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, oauth: { ...xiaomiOAuth, [key]: value } }, null, 2))
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
		if (type === 'xiaomi' && !Array.isArray(parsed.devices)) parsed.devices = []
		setSaving(true); setError(null); setFieldErrors({}); try { await onSave({ id, name, type, enabled, config: parsed }, Boolean(provider)) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败'); if (cause instanceof ApiError) setFieldErrors(cause.fields) } finally { setSaving(false) }
  }
	async function testConnection() {
		let parsed: Record<string, unknown>
		try { parsed = JSON.parse(config) as Record<string, unknown>; if (!parsed || Array.isArray(parsed)) throw new Error() } catch { setError('扩展配置必须是 JSON 对象'); return }
		if (!onTest) return
		setTesting(true); setError(null); setTestResult(null)
		try { await onTest({ id, name, type, enabled, config: parsed }); setTestResult('连接成功，订阅已建立。') } catch (cause) { setError(cause instanceof Error ? cause.message : '连接测试失败') } finally { setTesting(false) }
	}
	return <div className="modal-backdrop"><form className="target-form" role="dialog" aria-modal="true" aria-labelledby="provider-form-title" onSubmit={(event) => void submit(event)}>
		<div className="form-heading"><div><p className="eyebrow">PROVIDER</p><h2 id="provider-form-title">{provider ? '编辑 Provider' : '新建 Provider'}</h2></div><button type="button" onClick={onCancel}>关闭</button></div>
	    <div className="form-grid"><label>ID（留空自动生成）<input aria-invalid={Boolean(fieldErrors.id)} value={id} disabled={Boolean(provider)} onChange={(event) => setID(event.target.value)} placeholder={type === 'mqtt' ? 'mqtt-main' : type === 'xiaomi' ? 'xiaomi-main' : 'virtual-lab'} />{fieldErrors.id && <small className="field-error">{fieldErrors.id}</small>}</label><label>名称<input aria-invalid={Boolean(fieldErrors.name)} value={name} onChange={(event) => setName(event.target.value)} placeholder={type === 'mqtt' ? '家庭 MQTT' : type === 'xiaomi' ? '米家中枢网关' : '实验室虚拟设备'} />{fieldErrors.name && <small className="field-error">{fieldErrors.name}</small>}</label><label className="wide">类型<select aria-invalid={Boolean(fieldErrors.type)} value={type} disabled={Boolean(provider)} onChange={(event) => { const next = event.target.value; setType(next); if (!provider) { const selected = next === 'mqtt' ? mqttExample : next === 'xiaomi' ? xiaomiExample : example; setConfig(JSON.stringify(selected, null, 2)) } }}><option value="virtual">Virtual</option><option value="mqtt">MQTT</option><option value="xiaomi">Xiaomi Central Hub</option></select>{fieldErrors.type && <small className="field-error">{fieldErrors.type}</small>}</label>
	{type === 'mqtt' ? <div className="wide mqtt-config-grid">
		<label>Broker URL<input aria-label="MQTT Broker URL" required value={String(configObject.brokerUrl ?? '')} onChange={(event) => updateMQTT('brokerUrl', event.target.value)} placeholder="mqtt://192.168.1.10:1883" /></label>
		<label>Topic Prefix<input aria-label="MQTT Topic Prefix" value={String(configObject.topicPrefix ?? 'homeloom')} onChange={(event) => updateMQTT('topicPrefix', event.target.value)} /></label>
		<label>用户名<input aria-label="MQTT 用户名" value={String(configObject.username ?? '')} onChange={(event) => updateMQTT('username', event.target.value)} autoComplete="username" /></label>
		<label>密码<input aria-label="MQTT 密码" type="password" value={String(configObject.password ?? '')} onChange={(event) => updateMQTT('password', event.target.value)} autoComplete="new-password" />{hasRedactedSecrets && <small>保持 ******** 可沿用数据库中的密码。</small>}</label>
		<label>Client ID<input aria-label="MQTT Client ID" value={String(configObject.clientId ?? '')} onChange={(event) => updateMQTT('clientId', event.target.value)} placeholder="留空自动生成" /></label>
		<label>QoS<select aria-label="MQTT QoS" value={Number(configObject.qos ?? 1)} onChange={(event) => updateMQTT('qos', Number(event.target.value))}><option value={0}>0</option><option value={1}>1</option><option value={2}>2</option></select></label>
		<label>Keep Alive（秒）<input aria-label="MQTT Keep Alive" type="number" min="5" max="3600" value={Number(configObject.keepAliveSeconds ?? 30)} onChange={(event) => updateMQTT('keepAliveSeconds', Number(event.target.value))} /></label>
		<label>连接超时（秒）<input aria-label="MQTT 连接超时" type="number" min="1" max="120" value={Number(configObject.connectTimeoutSeconds ?? 10)} onChange={(event) => updateMQTT('connectTimeoutSeconds', Number(event.target.value))} /></label>
		<label>Session 保留（秒）<input aria-label="MQTT Session 保留" type="number" min="1" value={Number(configObject.sessionExpirySeconds ?? 86400)} onChange={(event) => updateMQTT('sessionExpirySeconds', Number(event.target.value))} /></label>
		<label>Retained State 最大年龄（秒）<input aria-label="MQTT Retained State 最大年龄" type="number" min="1" max="86400" value={Number(configObject.retainedStateMaxAgeSeconds ?? 300)} onChange={(event) => updateMQTT('retainedStateMaxAgeSeconds', Number(event.target.value))} /></label>
		<details className="wide"><summary>TLS / mTLS 高级设置</summary><div className="mqtt-tls-grid"><label>CA 文件<input aria-label="MQTT CA 文件" value={String(tlsConfig.caFile ?? '')} onChange={(event) => updateTLS('caFile', event.target.value)} /></label><label>Server Name<input aria-label="MQTT TLS Server Name" value={String(tlsConfig.serverName ?? '')} onChange={(event) => updateTLS('serverName', event.target.value)} /></label><label>客户端证书<input aria-label="MQTT 客户端证书" value={String(tlsConfig.certFile ?? '')} onChange={(event) => updateTLS('certFile', event.target.value)} /></label><label>客户端私钥<input aria-label="MQTT 客户端私钥" value={String(tlsConfig.keyFile ?? '')} onChange={(event) => updateTLS('keyFile', event.target.value)} /></label><label className="enable-row"><input type="checkbox" checked={Boolean(tlsConfig.insecureSkipVerify)} onChange={(event) => updateTLS('insecureSkipVerify', event.target.checked)} />跳过证书校验（仅调试）</label></div></details>
		{fieldErrors.config && <small className="field-error wide">{fieldErrors.config}</small>}<small className="wide">订阅 discovery、state 和 availability；控制命令发布到 command Topic。配置保存到 SQLite，密码由主密钥加密且由 API 脱敏返回。</small>
		{onTest && <button type="button" className="example-button" disabled={testing || saving} onClick={() => void testConnection()}>{testing ? '连接中…' : '测试连接'}</button>}{testResult && <small className="wide test-success">{testResult}</small>}
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
				<button type="button" className="example-button" disabled={discoveringGateways || !configObject.clientCertificate} onClick={() => void discoverGateways()}>{discoveringGateways ? '发现中…' : '局域网发现中枢'}</button>
				<label>轮询校准（秒）<input aria-label="小米轮询校准间隔" type="number" min="5" max="3600" disabled={!configObject.clientCertificate} value={Number(configObject.pollIntervalSeconds ?? 60)} onChange={(event) => updateXiaomi('pollIntervalSeconds', Number(event.target.value))} /></label>
				{gateways.length > 0 && <div className="wide"><small>选择支持 MQTT 的主中枢：</small><div className="simulation-actions">{gateways.map((gateway) => <button type="button" key={`${gateway.instance}-${gateway.hostName}`} disabled={!gateway.mqttEnabled} onClick={() => setConfig(JSON.stringify({ ...configObject, host: gateway.addresses[0] ?? gateway.hostName, port: gateway.port }, null, 2))}>{gateway.instance} · {gateway.addresses[0] ?? gateway.hostName} · role {gateway.role ?? '—'}</button>)}</div></div>}
				{onTest && <button type="button" className="example-button" disabled={testing || saving || authorizing || !configObject.clientCertificate || !configObject.host} onClick={() => void testConnection()}>{testing ? '连接中…' : '测试中枢 MQTT'}</button>}
			</div></section>
			<div className="xiaomi-next-step"><strong>03 · 子设备配置</strong><p>保存并启用 Provider，确认 MQTT 状态为“running”后，在米家页面进入独立的“管理子设备”页面。这里不再读取或编辑设备映射。</p></div>
			<details><summary>连接与证书高级设置</summary><div className="mqtt-tls-grid"><label>Virtual DID<input aria-label="小米 Virtual DID" readOnly value={String(configObject.clientId ?? xiaomiOAuth.virtualDid ?? '')} /></label><label>TLS Server Name（仅用于 SNI）<input aria-label="小米 TLS Server Name" value={String(configObject.serverName ?? '')} onChange={(event) => updateXiaomi('serverName', event.target.value)} /><small>不会用于 DNS/IP SAN 校验。</small></label><label className="enable-row"><input type="checkbox" checked={Boolean(configObject.insecureSkipVerify)} onChange={(event) => updateXiaomi('insecureSkipVerify', event.target.checked)} />跳过服务端证书校验（仅诊断）</label><small>{configObject.clientCertificate ? '默认验证小米 CA 链、证书有效期和 ServerAuth；不验证 DNS/IP SAN。' : '尚未申请中枢客户端证书。'}</small></div></details>
			{hasRedactedSecrets && <small>Token 和私钥已由 API 脱敏；保持 ******** 即可沿用数据库中的加密值。</small>}{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{testResult && <small className="test-success">{testResult}</small>}
		</div> : <label className="wide config-editor"><span>扩展配置（JSON）</span><textarea aria-invalid={Boolean(fieldErrors.config)} rows={11} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{hasRedactedSecrets && <small>敏感字段已显示为 ********；保持占位符即可沿用数据库中的原值，输入新值可进行替换。</small>}<small>支持 10 种统一模型及必须/可选参数；未进入标准契约的属性会标记为自定义参数。</small><button type="button" className="example-button" onClick={() => setConfig(JSON.stringify(example, null, 2))}>载入完整模型示例</button></label>}</div>
    <label className="enable-row"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />立即启用（无需重启服务）</label>
    {error && <p className="inline-error">{error}</p>}<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '应用中…' : '保存并应用'}</button></div>
  </form></div>
}
