import { useMemo, useState } from 'react'
import { discoverXiaomiDevices, type XiaomiHubDevice } from '../api/xiaomi'
import type { Provider, ProviderInput } from '../types/provider'
import { inferXiaomiDeviceType, requiredXiaomiProperties, stableXiaomiID, xiaomiDeviceTypes } from '../xiaomiMappings'

function configuredMappings(provider: Provider): Array<Record<string, unknown>> {
	return Array.isArray(provider.config.devices) ? provider.config.devices.filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object' && !Array.isArray(item))) : []
}

export function XiaomiDeviceManager({ provider, onClose, onSave }: {
	provider: Provider
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
}) {
	const initialMappings = useMemo(() => configuredMappings(provider), [provider])
	const [mappings, setMappings] = useState<Array<Record<string, unknown>>>(initialMappings)
	const [mappingJSON, setMappingJSON] = useState(JSON.stringify(initialMappings, null, 2))
	const [hubDevices, setHubDevices] = useState<XiaomiHubDevice[]>([])
	const [deviceTypes, setDeviceTypes] = useState<Record<string, string>>({})
	const [connectionModes, setConnectionModes] = useState<Record<string, string>>({})
	const [discovering, setDiscovering] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const connected = provider.enabled && provider.status === 'running'
	const mappedDIDs = new Set(mappings.map((item) => String(item.did ?? '')).filter(Boolean))

	function replaceMappings(next: Array<Record<string, unknown>>) {
		setMappings(next)
		setMappingJSON(JSON.stringify(next, null, 2))
	}

	async function discover() {
		setDiscovering(true); setError(null); setResult(null)
		try {
			const items = await discoverXiaomiDevices(provider.id, provider.type)
			setHubDevices(items)
			setDeviceTypes(Object.fromEntries(items.map((item) => [item.did, inferXiaomiDeviceType(item)])))
			setConnectionModes(Object.fromEntries(items.map((item) => [item.did, 'auto'])))
			setResult(items.length ? `已通过当前${provider.type === 'xiaomi' ? '中枢 MQTT 连接' : '第三方 MIoT 云会话'}读取 ${items.length} 台设备。` : '设备来源返回了空目录。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取小米设备目录') } finally { setDiscovering(false) }
	}

	function addDevice(item: XiaomiHubDevice) {
		if (mappedDIDs.has(item.did)) return
		const type = deviceTypes[item.did] ?? inferXiaomiDeviceType(item)
		const connectionMode = connectionModes[item.did] ?? 'auto'
		const id = provider.type === 'xiaomi-miot-cloud' ? stableXiaomiID(item.did).replace(/^xiaomi-/, 'xiaomi-miot-') : stableXiaomiID(item.did)
		replaceMappings([...mappings, { did: item.did, id, name: item.name || item.did, type, model: item.model ?? '', homeId: item.homeId ?? '', home: item.homeName ?? '', roomId: item.roomId ?? '', room: item.roomName ?? '', connectionMode: provider.type === 'xiaomi-miot-cloud' ? connectionMode : undefined, properties: requiredXiaomiProperties(type), actions: [] }])
	}

	function removeDevice(did: string) {
		replaceMappings(mappings.filter((item) => String(item.did ?? '') !== did))
	}

	async function save() {
		let parsed: unknown
		try { parsed = JSON.parse(mappingJSON) } catch { setError('设备与 MIoT 映射必须是有效 JSON'); return }
		if (!Array.isArray(parsed)) { setError('设备与 MIoT 映射必须是 JSON 数组'); return }
		setSaving(true); setError(null); setResult(null)
		try {
			await onSave({ id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled, config: { ...provider.config, devices: parsed } }, true)
			const next = parsed.filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object' && !Array.isArray(item)))
			setMappings(next)
			setResult(`已保存 ${next.length} 台子设备映射并实时应用。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '保存子设备映射失败') } finally { setSaving(false) }
	}

	const central = provider.type === 'xiaomi'
	return <section className="xiaomi-device-manager">
		<header><div><p className="eyebrow">XIAOMI · {central ? 'CENTRAL HUB' : 'MIOT CLOUD'}</p><h3>{provider.name} · {central ? '子设备' : '云端设备'}</h3><p>{central ? '子设备目录只通过已经建立的 MQTT 连接读取。' : '设备目录通过独立的第三方兼容 MIoT 云会话读取。'}映射保存后进入统一设备模型。</p></div><button onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? (central ? 'MQTT 已连接' : '云会话可用') : (central ? 'MQTT 尚未连接' : '云会话尚未连接')}</strong><small>{central ? `${provider.id} · ${String(provider.config.host || '未配置中枢')}:${Number(provider.config.port || 8883)}` : `${provider.id} · ${String(provider.config.region || 'cn').toUpperCase()} · 第三方兼容接口`}</small></div><button disabled={!connected || discovering} onClick={() => void discover()}>{discovering ? '正在读取…' : hubDevices.length ? '刷新设备目录' : central ? '从中枢读取子设备' : '从 MIoT 云读取设备'}</button></div>
		{!connected && <p className="inline-error" role="alert">请先完成{central ? ' OAuth、证书和 MQTT' : '小米账号或会话凭据'}配置并启用 Provider；状态变为 running 后才能读取设备目录。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success">{result}</p>}
		<div className="xiaomi-device-binding">
			<div className="xiaomi-device-binding__heading"><div><strong>{central ? '中枢子设备目录' : 'MIoT 云设备目录'}</strong><small>选择统一模型并加入映射；已映射设备可以在此移除。</small></div><span>{hubDevices.length} 台已读取</span></div>
			{hubDevices.length === 0 ? <p className="xiaomi-device-binding__empty">连接就绪后读取设备目录，再选择需要作为来源发布的设备。</p> : <div className="xiaomi-hub-device-list">{hubDevices.map((item) => { const mapped = mappedDIDs.has(item.did); const location = `${item.homeName || '未知家庭'} / ${item.roomName || '未分配房间'}`; return <article key={item.did}><div><strong>{item.name || item.did}</strong><small>{location} · {item.model || item.specType || '型号未知'}</small>{!central && <small>{item.localAvailable ? `局域网 MIoT 可用 · ${item.localIp}` : '未取得局域网 IP/Token，将使用云端'}</small>}<code>{item.did}</code></div><label>统一模型（deviceType）<select aria-label={`${item.name || item.did} 统一模型`} value={deviceTypes[item.did] ?? inferXiaomiDeviceType(item)} disabled={mapped} onChange={(event) => setDeviceTypes((current) => ({ ...current, [item.did]: event.target.value }))}>{xiaomiDeviceTypes.map(([value, label]) => <option value={value} key={value}>{label}（{value}）</option>)}</select></label>{!central && <label>连接策略（connectionMode）<select aria-label={`${item.name || item.did} 连接策略`} value={connectionModes[item.did] ?? 'auto'} disabled={mapped} onChange={(event) => setConnectionModes((current) => ({ ...current, [item.did]: event.target.value }))}><option value="auto">自动：本地优先，云端回退（auto）</option><option value="local" disabled={!item.localAvailable}>仅局域网（local）</option><option value="cloud">仅云端（cloud）</option></select></label>}{mapped ? <button type="button" className="is-danger" onClick={() => removeDevice(item.did)}>移除映射</button> : <button type="button" onClick={() => addDevice(item)}>加入映射</button>}</article> })}</div>}
			<div className="xiaomi-mapped-summary"><strong>已映射 {mappings.length} 台设备</strong><small>自动模板只覆盖统一模型必需参数，仍需按具体型号核对 SIID、PIID 和 AIID。</small></div>
		</div>
		<details><summary>设备与 MIoT 映射（高级 JSON）</summary><label className="config-editor"><textarea aria-label="小米设备映射" rows={16} value={mappingJSON} onChange={(event) => { setMappingJSON(event.target.value); try { const parsed = JSON.parse(event.target.value) as unknown; if (Array.isArray(parsed)) setMappings(parsed.filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object' && !Array.isArray(item)))) } catch { /* save reports invalid JSON */ } }} spellCheck={false} /><small>属性使用 siid/piid，Action 使用 siid/aiid。连接与账号凭据不在本页面编辑。</small></label></details>
		<div className="form-actions"><button onClick={onClose}>取消</button><button className="primary" disabled={saving || !connected} onClick={() => void save()}>{saving ? '正在应用…' : '保存子设备映射'}</button></div>
	</section>
}
