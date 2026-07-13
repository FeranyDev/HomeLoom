import { useState } from 'react'
import type { Provider, ProviderInput } from '../types/provider'
import { ApiError } from '../api/client'

export function ProviderForm({ provider, onCancel, onSave, onTest }: { provider: Provider | null; onCancel: () => void; onSave: (input: ProviderInput, editing: boolean) => Promise<void>; onTest?: (input: ProviderInput) => Promise<void> }) {
  const [id, setID] = useState(provider?.id ?? '')
  const [name, setName] = useState(provider?.name ?? '')
  const [type, setType] = useState(provider?.type ?? 'virtual')
  const [enabled, setEnabled] = useState(provider?.enabled ?? true)
  const [config, setConfig] = useState(JSON.stringify(provider?.config ?? {}, null, 2))
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
	const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
	const hasRedactedSecrets = Boolean(provider && JSON.stringify(provider.config).includes('********'))
  const example = { latencyMs: 0, rejectWrites: false, devices: [{ id: 'demo-switch', name: '客厅开关', type: 'switch', online: true, power: false }, { id: 'demo-light', name: '客厅灯', type: 'lightbulb', online: true, power: true, brightness: 80, colorTemperature: 250, hue: 35, saturation: 45 }, { id: 'demo-outlet', name: '书房插座', type: 'outlet', online: true, power: false, inUse: false, currentPower: 0, energy: 1.25 }, { id: 'demo-temperature', name: '客厅温度', type: 'temperature-sensor', online: true, temperature: 23.6, batteryLevel: 96, lowBattery: false, tampered: false }, { id: 'demo-humidity', name: '客厅湿度', type: 'humidity-sensor', online: true, humidity: 56.2, batteryLevel: 92, lowBattery: false, tampered: false }, { id: 'demo-contact', name: '入户门', type: 'contact-sensor', online: true, contact: false, batteryLevel: 88, lowBattery: false, tampered: false }, { id: 'demo-motion', name: '走廊活动', type: 'motion-sensor', online: true, motion: false, batteryLevel: 84, lowBattery: false, tampered: false }, { id: 'demo-fan', name: '卧室风扇', type: 'fan', online: true, active: false, speed: 35, mode: 'manual', swingMode: true, direction: 'clockwise', controlLock: false }, { id: 'demo-air', name: '客厅净化器', type: 'air-purifier', online: true, active: true, speed: 60, mode: 'auto', swingMode: false, controlLock: false, airQuality: 'good', pm25: 12, voc: 80, filterLife: 82, filterChange: false }, { id: 'demo-shade', name: '南窗帘', type: 'window-covering', online: true, position: 50, obstruction: false }] }
	const mqttExample = { brokerUrl: 'mqtt://127.0.0.1:1883', username: '', password: '', clientId: '', topicPrefix: 'homeloom', qos: 1, keepAliveSeconds: 30, connectTimeoutSeconds: 10, sessionExpirySeconds: 86400, retainedStateMaxAgeSeconds: 300, tls: {} }
	let configObject: Record<string, unknown> = {}
	try { const parsed = JSON.parse(config) as unknown; if (parsed && !Array.isArray(parsed) && typeof parsed === 'object') configObject = parsed as Record<string, unknown> } catch { /* validation is shown on submit */ }
	const tlsConfig = configObject.tls && !Array.isArray(configObject.tls) && typeof configObject.tls === 'object' ? configObject.tls as Record<string, unknown> : {}
	const updateMQTT = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, [key]: value }, null, 2))
	const updateTLS = (key: string, value: unknown) => setConfig(JSON.stringify({ ...configObject, tls: { ...tlsConfig, [key]: value } }, null, 2))
  async function submit(event: React.FormEvent) {
    event.preventDefault(); let parsed: Record<string, unknown>
		try { parsed = JSON.parse(config) as Record<string, unknown>; if (!parsed || Array.isArray(parsed)) throw new Error() } catch { setError('扩展配置必须是 JSON 对象'); setFieldErrors({ config: '必须是 JSON 对象' }); return }
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
    <div className="form-grid"><label>ID（留空自动生成）<input aria-invalid={Boolean(fieldErrors.id)} value={id} disabled={Boolean(provider)} onChange={(event) => setID(event.target.value)} placeholder={type === 'mqtt' ? 'mqtt-main' : 'virtual-lab'} />{fieldErrors.id && <small className="field-error">{fieldErrors.id}</small>}</label><label>名称<input aria-invalid={Boolean(fieldErrors.name)} value={name} onChange={(event) => setName(event.target.value)} placeholder={type === 'mqtt' ? '家庭 MQTT' : '实验室虚拟设备'} />{fieldErrors.name && <small className="field-error">{fieldErrors.name}</small>}</label><label className="wide">类型<select aria-invalid={Boolean(fieldErrors.type)} value={type} disabled={Boolean(provider)} onChange={(event) => { const next = event.target.value; setType(next); if (!provider) setConfig(JSON.stringify(next === 'mqtt' ? mqttExample : example, null, 2)) }}><option value="virtual">Virtual</option><option value="mqtt">MQTT</option></select>{fieldErrors.type && <small className="field-error">{fieldErrors.type}</small>}</label>
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
	</div> : <label className="wide config-editor"><span>扩展配置（JSON）</span><textarea aria-invalid={Boolean(fieldErrors.config)} rows={11} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} />{fieldErrors.config && <small className="field-error">{fieldErrors.config}</small>}{hasRedactedSecrets && <small>敏感字段已显示为 ********；保持占位符即可沿用数据库中的原值，输入新值可进行替换。</small>}<small>支持 10 种统一模型及必须/可选参数；未进入标准契约的属性会标记为自定义参数。</small><button type="button" className="example-button" onClick={() => setConfig(JSON.stringify(example, null, 2))}>载入完整模型示例</button></label>}</div>
    <label className="enable-row"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />立即启用（无需重启服务）</label>
    {error && <p className="inline-error">{error}</p>}<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '应用中…' : '保存并应用'}</button></div>
  </form></div>
}
