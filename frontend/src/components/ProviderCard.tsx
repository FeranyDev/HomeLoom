import { useState } from 'react'
import { availabilityLabel, deviceProperty, runtimeModeLabel, type Device, type DeviceAvailability } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'

type SimulationValues = { availability?: DeviceAvailability; online?: boolean; power?: boolean; value?: number; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; active?: boolean; speed?: number; mode?: string; filterLife?: number; filterChange?: boolean; position?: number; sequence?: number; repeat?: number }

function propertyBool(device: Device, capability: string, property: string): boolean { return deviceProperty(device, capability, property)?.bool ?? false }
function propertyNumber(device: Device, capability: string, property: string): number { return deviceProperty(device, capability, property)?.number ?? 0 }
function propertyInt(device: Device, capability: string, property: string): number { return deviceProperty(device, capability, property)?.int ?? 0 }
function propertyString(device: Device, capability: string, property: string): string { return deviceProperty(device, capability, property)?.string ?? '' }
function objectValue(value: unknown): Record<string, unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {} }
function arrayValue(value: unknown): unknown[] { return Array.isArray(value) ? value : [] }
function dateTime(value?: string): string { return value ? new Date(value).toLocaleString() : '未知' }

function providerTypeName(type: string): string {
	if (type === 'xiaomi') return '小米中枢网关'
	if (type === 'xiaomi-miot-cloud') return '小米 MIoT 云 · 第三方兼容'
	if (type === 'mqtt') return 'MQTT · HomeLoom v1'
	if (type === 'virtual') return 'Virtual Runtime'
	return type
}

export function ProviderCard({ provider, devices, onEdit, onDelete, onRestart, onSimulate, onManageDevices, onTest }: {
	provider: Provider
	devices: Device[]
	onEdit: (provider: Provider) => void
	onDelete: (provider: Provider) => void
	onRestart: (provider: Provider) => Promise<void>
	onSimulate: (device: Device, values: SimulationValues) => Promise<void>
	onManageDevices?: (provider: Provider) => void
	onTest?: (provider: ProviderInput) => Promise<void>
}) {
	const config = objectValue(provider.config)
	const oauth = objectValue(config.oauth)
	const configuredDevices = arrayValue(config.devices)
	const features = Object.entries(provider.capabilities || {}).filter(([, enabled]) => enabled).map(([name]) => name)
	const onlineDevices = devices.filter((item) => item.availability === 'online').length
	const authorized = Boolean(oauth.clientId && oauth.oauthUuid && oauth.virtualDid)
	const certificateReady = Boolean(config.clientId && config.clientCertificate && config.privateKey)
	const mqttMode = String(config.mode ?? 'client') === 'server' ? 'server' : 'client'
	const displayedProviderType = provider.type === 'mqtt' ? `mqtt-${mqttMode}` : provider.type
	const brokerReady = mqttMode === 'server' ? Boolean(config.listenAddress) : Boolean(config.brokerUrl)
	const cloudSessionReady = Boolean((config.username && config.password) || (config.userId && config.ssecurity && config.serviceToken))
	const setupReady = provider.type === 'xiaomi' ? authorized : provider.type === 'xiaomi-miot-cloud' ? cloudSessionReady : provider.type === 'mqtt' ? brokerReady : configuredDevices.length > 0
	const setupLabel = provider.type === 'xiaomi' ? (authorized ? '已授权' : '待授权') : provider.type === 'xiaomi-miot-cloud' ? (cloudSessionReady ? '账号就绪' : '待登录') : provider.type === 'mqtt' ? (brokerReady ? (mqttMode === 'server' ? '监听就绪' : 'Broker 就绪') : '待配置') : `${configuredDevices.length} 台`
	const setupDetail = provider.type === 'xiaomi' ? `${String(oauth.region || 'cn').toUpperCase()} · UID ${String(oauth.uid || '—')}` : provider.type === 'xiaomi-miot-cloud' ? `${String(config.region || 'cn').toUpperCase()} · ${String(config.username || config.userId || '未配置账号')}` : provider.type === 'mqtt' ? mqttMode === 'server' ? String(config.listenAddress || '尚未设置监听地址') : String(config.brokerUrl || '尚未设置 Broker') : '数据库期望设备'
	const connectionReady = provider.status === 'running'
	const connectionLabel = provider.type === 'xiaomi' ? (certificateReady ? '证书就绪' : '待申请证书') : provider.type === 'xiaomi-miot-cloud' ? (connectionReady ? '云会话可用' : provider.status) : provider.type === 'mqtt' ? (connectionReady ? (mqttMode === 'server' ? '服务端监听中' : 'Broker 已连接') : provider.status) : connectionReady ? '已连接' : provider.status
	const connectionDetail = provider.type === 'xiaomi' ? `${String(config.host || '未选择中枢')}:${Number(config.port || 8883)} · MQTT 本地优先 / OAuth 官方云回退` : provider.type === 'xiaomi-miot-cloud' ? `轮询 ${Number(config.pollIntervalSeconds || 30)} 秒 · 非官方兼容接口` : provider.type === 'mqtt' ? mqttMode === 'server' ? '外部设备主动连接 · 内嵌 Broker' : `${String(config.clientId || `homeloom-${provider.id}`)} · MQTT 客户端会话` : '进程内异步事件源'
	const cloudMqttLabel = provider.metrics?.cloudMqttConnected ? '已连接' : provider.metrics?.cloudMqttConfigured ? '重连中' : '未配置'
	const managedDeviceSource = provider.type === 'mqtt' || provider.type === 'xiaomi' || provider.type === 'xiaomi-miot-cloud'
	const [drafts, setDrafts] = useState<Record<string, string>>({})
	const [busy, setBusy] = useState<'test' | 'restart' | null>(null)

	async function run(action: 'test' | 'restart') {
		setBusy(action)
		try { if (action === 'test' && onTest) await onTest(provider); else if (action === 'restart') await onRestart(provider) } finally { setBusy(null) }
	}

	const numericControl = (item: Device, field: 'value' | 'temperature' | 'humidity' | 'speed' | 'filterLife' | 'position', value: number) => {
		const key = `${item.id}:${field}`
		const label = { value: '传感器值', temperature: '温度', humidity: '湿度', speed: '速度', filterLife: '滤芯寿命', position: '位置' }[field]
		return <><input aria-label={`${item.name}${label}`} type="number" min={field === 'value' ? undefined : field === 'temperature' ? -100 : 0} max={field === 'value' ? undefined : field === 'temperature' ? 200 : 100} step={field === 'position' ? 1 : 0.1} value={drafts[key] ?? String(value)} onChange={(event) => setDrafts((current) => ({ ...current, [key]: event.target.value }))} /><button onClick={() => { const next = Number(drafts[key] ?? value); void onSimulate(item, { [field]: next } as SimulationValues) }}>上报</button></>
	}

	return <article className="provider-card provider-runtime-card">
		<header>
			<div><div className="device-card__topline"><span className={`status-dot ${connectionReady ? 'is-online' : ''}`} />{provider.status}<span className="provider">{displayedProviderType}</span></div><h3>{provider.name}</h3><p>{provider.type === 'mqtt' ? `MQTT ${mqttMode === 'server' ? '服务端（SERVER）' : '客户端（CLIENT）'} · HomeLoom v1` : providerTypeName(provider.type)} · {provider.id}</p></div>
			<div className="provider-card__actions"><button onClick={() => onEdit(provider)}>{provider.type === 'xiaomi' ? '账号与中枢' : provider.type === 'xiaomi-miot-cloud' ? '云账号配置' : provider.type === 'mqtt' ? (mqttMode === 'server' ? '监听配置' : 'Broker 配置') : '配置'}</button>{managedDeviceSource && onManageDevices && <button disabled={!connectionReady} title={connectionReady ? '使用当前运行连接配置设备' : '请先连接设备来源'} onClick={() => onManageDevices(provider)}>管理设备</button>}{onTest && <button disabled={busy !== null || (provider.type === 'xiaomi' && !certificateReady)} onClick={() => void run('test')}>{busy === 'test' ? '测试中…' : provider.type === 'mqtt' && mqttMode === 'server' ? '测试监听' : '测试连接'}</button>}{provider.enabled && <button disabled={busy !== null} onClick={() => void run('restart')}>{busy === 'restart' ? (provider.type === 'mqtt' && mqttMode === 'server' ? '重启监听中…' : '重连中…') : provider.type === 'mqtt' && mqttMode === 'server' ? '重启监听' : '重新连接'}</button>}<button className="is-danger" onClick={() => onDelete(provider)}>删除</button></div>
		</header>
		<div className="provider-status-grid">
			<div><span>{provider.type === 'xiaomi' || provider.type === 'xiaomi-miot-cloud' ? '账号与凭据' : provider.type === 'mqtt' ? (mqttMode === 'server' ? '服务端配置' : 'Broker 配置') : '设备配置'}</span><strong className={setupReady ? 'is-ready' : ''}>{setupLabel}</strong><small>{setupDetail}</small></div>
			<div><span>运行连接</span><strong className={connectionReady ? 'is-ready' : ''}>{connectionLabel}</strong><small>{connectionDetail}</small></div>
			<div><span>期望设备</span><strong>{configuredDevices.length}</strong><small>{provider.type === 'mqtt' ? '数据库设备路由 · 严格白名单' : '数据库配置'}</small></div>
			<div><span>实时发布</span><strong>{onlineDevices} / {devices.length}</strong><small>当前内存快照</small></div>
		</div>
		<div className="provider-runtime"><span>能力 <b>{features.length}</b></span>{provider.type === 'mqtt' ? <><span>消息 <b>{provider.metrics?.messagesReceived ?? 0}</b></span><span>无效 <b>{provider.metrics?.messagesInvalid ?? 0}</b></span><span>丢弃 <b>{provider.metrics?.messagesDropped ?? 0}</b></span><span>命令 <b>{provider.metrics?.commandsPublished ?? 0}</b></span></> : <><span>请求 <b>{provider.metrics?.requests ?? 0}</b></span><span>事件 <b>{provider.metrics?.events ?? 0}</b></span><span>错误 <b>{provider.metrics?.errors ?? 0}</b></span>{provider.type === 'xiaomi' && <><span>本地 <b>{provider.metrics?.localRequests ?? 0}</b></span><span>转云 <b>{provider.metrics?.cloudFallbacks ?? 0}</b></span><span>云请求 <b>{provider.metrics?.cloudRequests ?? 0}</b></span><span>官方云 MQTT <b>{cloudMqttLabel}</b></span><span>云推送 <b>{provider.metrics?.cloudMqttMessagesReceived ?? 0}</b></span></>}</>}</div>
		{provider.credentials?.managed && <div className="provider-credential-status"><div><strong>凭据自动续期</strong><small>下次检查 {dateTime(provider.credentials.refreshAt)}</small></div><span>Token 到期 <b>{dateTime(provider.credentials.tokenExpiresAt)}</b></span><span>证书到期 <b>{dateTime(provider.credentials.certificateExpiresAt)}</b></span>{provider.credentialError && <p role="alert">续期失败：{provider.credentialError} · {dateTime(provider.credentialRetryAt)} 重试</p>}</div>}
		{provider.error && <div className="provider-error"><p className="inline-error">{provider.error}</p><small>已自动重试 {provider.retryCount ?? 0} 次{provider.nextRetryAt ? ` · 下次 ${new Date(provider.nextRetryAt).toLocaleTimeString()}` : ''}</small></div>}
		<div className="provider-device-list"><div className="command-heading"><h3>已发布设备</h3><span>{devices.length} 台</span></div>{devices.length === 0 ? <p>实例连接并完成发现或设备配置后，设备会在这里显示。</p> : devices.map((item) => <div key={item.id}><span className={`status-dot is-${item.availability}`} /><strong>{item.name}</strong><code>{item.id}</code><small className="provider-device-runtime"><span>{item.type} · seq {item.sequence ?? '—'}</span>{(provider.type === 'xiaomi' || provider.type === 'xiaomi-miot-cloud') && item.runtimeMode && <i className={`device-runtime-mode is-${item.runtimeMode}`}>{runtimeModeLabel(item.runtimeMode, item.stateTransport)}</i>}</small></div>)}</div>
		{provider.type === 'virtual' && devices.length > 0 && <div className="simulation-panel"><div><strong>运行时模拟</strong><small>状态只保存在内存中，重启后按配置重建</small></div>{devices.map((item) => {
			const powered = item.type === 'switch' || item.type === 'lightbulb' || item.type === 'outlet'
			const power = propertyBool(item, 'switch', 'power'); const contact = propertyBool(item, 'contact', 'contact-detected'); const motion = propertyBool(item, 'motion', 'motion-detected'); const advancedCapability = item.type === 'fan' ? 'fan' : 'air-purifier'; const active = propertyBool(item, advancedCapability, 'active'); const mode = propertyString(item, advancedCapability, 'target-state'); const filterChange = propertyBool(item, 'filter', 'change-indication')
			return <div className="simulation-device" key={item.id}><div className="simulation-name"><span className={`status-dot is-${item.availability}`} /><b>{item.name}</b><small>{item.id} · {availabilityLabel(item.availability)} · seq {item.sequence ?? '—'}</small></div><div className="simulation-actions"><button onClick={() => void onSimulate(item, { online: !item.online })}>{item.online ? '设为离线' : '恢复在线'}</button><button onClick={() => void onSimulate(item, { availability: 'unknown' })}>设为未知</button><button onClick={() => void onSimulate(item, { repeat: 2 })}>重复事件</button><button onClick={() => void onSimulate(item, { sequence: Math.max(1, (item.sequence ?? 1) - 1) })}>旧序列事件</button>{powered && <button onClick={() => void onSimulate(item, { power: !power })}>{power ? '关闭' : '打开'}</button>}{item.type === 'single-property-sensor' && numericControl(item, 'value', propertyNumber(item, 'sensor', 'value'))}{item.type === 'temperature-humidity-sensor' && <>{numericControl(item, 'temperature', propertyNumber(item, 'temperature', 'current-temperature'))}{numericControl(item, 'humidity', propertyNumber(item, 'humidity', 'current-humidity'))}</>}{item.type === 'contact-sensor' && <button onClick={() => void onSimulate(item, { contact: !contact })}>{contact ? '设为打开' : '设为闭合'}</button>}{item.type === 'motion-sensor' && <button onClick={() => void onSimulate(item, { motion: !motion })}>{motion ? '清除活动' : '触发活动'}</button>}{(item.type === 'fan' || item.type === 'air-purifier') && <><button onClick={() => void onSimulate(item, { active: !active })}>{active ? '停止' : '启动'}</button><button onClick={() => void onSimulate(item, { mode: mode === 'auto' ? 'manual' : 'auto' })}>{mode === 'auto' ? '手动模式' : '自动模式'}</button>{numericControl(item, 'speed', propertyNumber(item, advancedCapability, 'rotation-speed'))}</>}{item.type === 'air-purifier' && <>{numericControl(item, 'filterLife', propertyNumber(item, 'filter', 'life-level'))}<button onClick={() => void onSimulate(item, { filterChange: !filterChange })}>{filterChange ? '标记滤芯正常' : '标记需换滤芯'}</button></>}{item.type === 'window-covering' && numericControl(item, 'position', propertyInt(item, 'window-covering', 'current-position'))}</div></div>
		})}</div>}
	</article>
}
