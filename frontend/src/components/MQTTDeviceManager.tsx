import { useMemo, useState } from 'react'
import type { Device } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'

type MQTTTopics = { discovery: string; availability: string; state: string; command: string }
type MQTTRoute = { id: string; topicPrefix: string; protocol: 'homeloom-v1'; qos: number; topics: MQTTTopics }

function objectValue(value: unknown): Record<string, unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {} }
function defaultTopics(prefix: string, id: string): MQTTTopics {
	return {
		discovery: `${prefix}/discovery/${id}`,
		availability: `${prefix}/availability/${id}`,
		state: `${prefix}/state/${id}/{endpointId}/{capabilityId}/{propertyId}`,
		command: `${prefix}/command/${id}/{endpointId}/{capabilityId}/{operationId}`,
	}
}
function configuredRoutes(config: Record<string, unknown>): MQTTRoute[] {
	if (!Array.isArray(config.devices)) return []
	return config.devices.flatMap((value) => {
		const item = objectValue(value); const id = String(item.id ?? '').trim(); const topicPrefix = String(item.topicPrefix ?? '').replace(/^\/+|\/+$/g, ''); if (!id || !topicPrefix) return []
		const topics = objectValue(item.topics); const defaults = defaultTopics(topicPrefix, id)
		return [{ id, topicPrefix, protocol: 'homeloom-v1' as const, qos: Number(item.qos ?? 1), topics: { discovery: String(topics.discovery ?? defaults.discovery), availability: String(topics.availability ?? defaults.availability), state: String(topics.state ?? defaults.state), command: String(topics.command ?? defaults.command) } }]
	})
}

const blank = { id: '', topicPrefix: '', qos: 1, discovery: '', availability: '', state: '', command: '' }

export function MQTTDeviceManager({ provider, devices, onClose, onSave }: { provider: Provider; devices: Device[]; onClose: () => void; onSave: (input: ProviderInput, editing: boolean) => Promise<void> }) {
	const [routes, setRoutes] = useState<MQTTRoute[]>(() => configuredRoutes(provider.config))
	const [draft, setDraft] = useState(blank)
	const [editingID, setEditingID] = useState<string | null>(null)
	const [error, setError] = useState<string | null>(null)
	const [saving, setSaving] = useState(false)
	const serverMode = String(provider.config.mode ?? 'client') === 'server'
	const published = useMemo(() => new Map(devices.map((item) => [item.id, item])), [devices])
	const normalizedPrefix = draft.topicPrefix.trim().replace(/^\/+|\/+$/g, '')
	const preview = defaultTopics(normalizedPrefix || 'homeloom', draft.id.trim() || 'device-id')

	function update(field: keyof typeof blank, value: string | number) { setDraft((current) => ({ ...current, [field]: value })) }
	function resetEditor() { setDraft(blank); setEditingID(null); setError(null) }
	function edit(item: MQTTRoute) {
		const defaults = defaultTopics(item.topicPrefix, item.id)
		setEditingID(item.id); setError(null); setDraft({ id: item.id, topicPrefix: item.topicPrefix, qos: item.qos, discovery: item.topics.discovery === defaults.discovery ? '' : item.topics.discovery, availability: item.topics.availability === defaults.availability ? '' : item.topics.availability, state: item.topics.state === defaults.state ? '' : item.topics.state, command: item.topics.command === defaults.command ? '' : item.topics.command })
	}
	function stageRoute(event: React.FormEvent) {
		event.preventDefault(); setError(null)
		const id = draft.id.trim(); const prefix = normalizedPrefix
		if (!/^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/.test(id)) { setError('设备 ID 必须是 1–64 位小写稳定标识，可使用数字、点、下划线和连字符。'); return }
		if (!prefix || /[+#{}\0]/.test(prefix) || prefix.split('/').some((level) => !level.trim())) { setError('Topic Prefix 必须由非空层级组成，不能包含 +、# 或模板括号。'); return }
		if (routes.some((item) => item.id === id && item.id !== editingID)) { setError(`设备 ID“${id}”已经存在。`); return }
		const defaults = defaultTopics(prefix, id)
		const topics = { discovery: draft.discovery.trim() || defaults.discovery, availability: draft.availability.trim() || defaults.availability, state: draft.state.trim() || defaults.state, command: draft.command.trim() || defaults.command }
		if (!['{endpointId}', '{capabilityId}', '{propertyId}'].every((token) => topics.state.includes(token))) { setError('State Topic 必须包含 endpointId、capabilityId 和 propertyId 三个占位符。'); return }
		if (!['{endpointId}', '{capabilityId}', '{operationId}'].every((token) => topics.command.includes(token))) { setError('Command Topic 必须包含 endpointId、capabilityId 和 operationId 三个占位符。'); return }
		const route: MQTTRoute = { id, topicPrefix: prefix, protocol: 'homeloom-v1', qos: Number(draft.qos), topics }
		setRoutes((current) => [...current.filter((item) => item.id !== editingID), route].sort((left, right) => left.id.localeCompare(right.id)))
		resetEditor()
	}
	async function save() {
		setSaving(true); setError(null)
		try { await onSave({ id: provider.id, type: provider.type, name: provider.name, enabled: provider.enabled, config: { ...provider.config, devices: routes } }, true) }
		catch (cause) { setError(cause instanceof Error ? cause.message : '保存 MQTT 设备失败') }
		finally { setSaving(false) }
	}

	return <section className="mqtt-device-manager">
		<header><div><p className="eyebrow">MQTT · {serverMode ? 'SERVER' : 'CLIENT'} · DEVICE ROUTES</p><h3>{provider.name} · 设备路由</h3><p>{serverMode ? '内嵌 Broker 监听保持运行；外部设备主动连接，并按逐设备白名单收发 Topic。' : '外部 Broker 连接保持不变；每台设备独立配置协议根主题、QoS 和收发 Topic。'}</p></div><button type="button" onClick={onClose}>返回 Provider</button></header>
		<div className="mqtt-device-manager__status"><span className={`status-dot ${provider.status === 'running' ? 'is-online' : ''}`} /><div><strong>{provider.status === 'running' ? (serverMode ? 'MQTT 服务端正在监听' : '外部 Broker 已连接') : `MQTT ${serverMode ? '服务端' : '客户端'} ${provider.status}`}</strong><small>{provider.id} · {serverMode ? String(provider.config.listenAddress || '未配置监听地址') : String(provider.config.brokerUrl || '未配置 Broker')} · {routes.length} 条设备路由</small></div><button type="button" disabled={saving || provider.status !== 'running'} onClick={() => void save()}>{saving ? '应用中…' : '保存并实时应用'}</button></div>
		{error && <p className="inline-error" role="alert">{error}</p>}
		<div className="mqtt-route-workbench">
			<form onSubmit={stageRoute} className="mqtt-route-editor">
				<div><strong>{editingID ? `编辑 ${editingID}` : '添加设备路由'}</strong><small>保存后订阅 retained Discovery，收到结构后才会发布设备。</small></div>
				<label>设备 ID（deviceId）<input aria-label="MQTT 设备 ID" value={draft.id} disabled={editingID !== null} onChange={(event) => update('id', event.target.value)} placeholder="living-room-light" /></label>
				<label>主题前缀（Topic Prefix）<input aria-label="MQTT 设备 Topic Prefix" value={draft.topicPrefix} onChange={(event) => update('topicPrefix', event.target.value)} placeholder="homeloom/living-room" /></label>
				<label>消息服务质量（QoS）<select aria-label="MQTT 设备 QoS" value={draft.qos} onChange={(event) => update('qos', Number(event.target.value))}><option value={0}>0 · 最多一次</option><option value={1}>1 · 至少一次</option><option value={2}>2 · 恰好一次</option></select></label>
				<label>协议（protocol）<select aria-label="MQTT 设备协议" value="homeloom-v1" disabled><option value="homeloom-v1">HomeLoom Device Protocol v1（homeloom-v1）</option></select></label>
				<details><summary>自定义完整 Topic</summary><div className="mqtt-topic-grid"><label>发现（discovery）<input aria-label="MQTT Discovery Topic" value={draft.discovery} onChange={(event) => update('discovery', event.target.value)} placeholder={preview.discovery} /></label><label>可用性（availability）<input aria-label="MQTT Availability Topic" value={draft.availability} onChange={(event) => update('availability', event.target.value)} placeholder={preview.availability} /></label><label>状态（state）<input aria-label="MQTT State Topic" value={draft.state} onChange={(event) => update('state', event.target.value)} placeholder={preview.state} /></label><label>命令（command）<input aria-label="MQTT Command Topic" value={draft.command} onChange={(event) => update('command', event.target.value)} placeholder={preview.command} /></label></div></details>
				<div className="simulation-actions"><button type="submit">{editingID ? '更新路由' : '加入设备'}</button>{editingID && <button type="button" onClick={resetEditor}>取消编辑</button>}</div>
			</form>
			<div className="mqtt-route-list"><div className="command-heading"><h3>已配置设备</h3><span>{routes.length} 台</span></div>{routes.length === 0 ? <p>{serverMode ? '尚未配置设备。服务端会保持监听，但 ACL 拒绝所有设备 Topic。' : '尚未配置设备。Provider 只会连接 Broker，不会订阅或发布任意设备。'}</p> : routes.map((item) => { const current = published.get(item.id); return <article key={item.id}><span className={`status-dot ${current?.availability === 'online' ? 'is-online' : ''}`} /><div><strong>{current?.name || item.id}</strong><small>{current ? `${current.type} · ${current.availability}` : '等待 retained Discovery'}</small><code>{item.id}</code></div><div><span className="parameter-level">QoS {item.qos}</span><code>{item.topicPrefix}</code></div><div className="simulation-actions"><button type="button" onClick={() => edit(item)}>编辑</button><button type="button" className="is-danger" onClick={() => setRoutes((currentRoutes) => currentRoutes.filter((route) => route.id !== item.id))}>移除</button></div></article> })}</div>
		</div>
		<div className="config-note"><span>Payload 契约</span><strong>HomeLoom Device Protocol v1</strong><p>Topic 路由可以逐设备调整；Discovery、State、Availability 和 Command 的 JSON 契约保持严格校验。来源属性进入设备中心后，再从该设备单独配置到统一模型的转换。</p></div>
	</section>
}
