import { useMemo, useState } from 'react'
import { ProviderDeviceAddFlow } from './ProviderDeviceAddFlow'
import type { Device } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'

type NetworkDeviceEntry = Record<string, unknown>

const deviceIDPattern = /^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/

function configuredDevices(provider: Provider): NetworkDeviceEntry[] {
	return Array.isArray(provider.config.devices)
		? provider.config.devices.filter((item): item is NetworkDeviceEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item))).map((item) => ({ ...item }))
		: []
}

function defaultDevice(index: number): NetworkDeviceEntry {
	return { id: `network-device-${index}`, name: '', host: '', probeMethod: 'tcp', probePort: 80, mac: '' }
}

function numberOrUndefined(value: string): number | undefined {
	return value === '' ? undefined : Number(value)
}

function normalizedEntry(draft: NetworkDeviceEntry, fallbackProbeMethod: string): { entry?: NetworkDeviceEntry; error?: string } {
	const id = String(draft.id ?? '').trim().toLowerCase()
	const name = String(draft.name ?? '').trim()
	const host = String(draft.host ?? '').trim()
	const probeMethod = String(draft.probeMethod ?? fallbackProbeMethod ?? 'tcp').trim().toLowerCase()
	const probePort = Number(draft.probePort ?? 0)
	if (!deviceIDPattern.test(id)) return { error: '设备 ID 必须是 1–64 位小写稳定标识，可使用数字、点、下划线和连字符。' }
	if (!name) return { error: '请输入网络设备名称。' }
	if (!host || host.startsWith('-')) return { error: '请输入有效的网络设备 Host。' }
	if (probeMethod !== 'tcp' && probeMethod !== 'icmp') return { error: '探测方式仅支持 TCP 或 ICMP。' }
	if (probeMethod === 'tcp' && (!Number.isInteger(probePort) || probePort < 1 || probePort > 65535)) return { error: 'TCP 探测端口必须是 1–65535 的整数。' }
	if (probeMethod === 'icmp' && (!Number.isInteger(probePort) || probePort < 0 || probePort > 65535)) return { error: 'ICMP 探测端口必须是 0–65535 的整数。' }
	const entry: NetworkDeviceEntry = { ...draft, id, name, host, probeMethod, probePort, mac: String(draft.mac ?? '').trim() }
	for (const key of ['probeIntervalSeconds', 'probeTimeoutSeconds', 'wakeGraceSeconds', 'onlineThreshold', 'offlineThreshold', 'wolPort'] as const) {
		if (entry[key] === '' || entry[key] === undefined || entry[key] === null) delete entry[key]
	}
	for (const key of ['wolBroadcastAddress', 'wolInterface'] as const) {
		if (!String(entry[key] ?? '').trim()) delete entry[key]
	}
	return { entry }
}

export function NetworkDeviceManager({ provider, devices, onClose, onSave }: {
	provider: Provider
	devices: Device[]
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
}) {
	const initialEntries = useMemo(() => configuredDevices(provider), [provider])
	const [entries, setEntries] = useState(initialEntries)
	const [entriesJSON, setEntriesJSON] = useState(JSON.stringify(initialEntries, null, 2))
	const [draft, setDraft] = useState<NetworkDeviceEntry>(() => defaultDevice(initialEntries.length + 1))
	const [editingID, setEditingID] = useState<string | null>(null)
	const [showEditor, setShowEditor] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const connected = provider.enabled && provider.status === 'running'
	const published = useMemo(() => new Map(devices.map((item) => [item.id, item])), [devices])
	const fallbackProbeMethod = String(provider.config.probeMethod ?? 'tcp')

	function replaceEntries(next: NetworkDeviceEntry[]) {
		setEntries(next)
		setEntriesJSON(JSON.stringify(next, null, 2))
	}
	function updateDraft(key: string, value: unknown) { setDraft((current) => ({ ...current, [key]: value })) }
	function beginAdd() {
		setDraft(defaultDevice(entries.length + 1)); setEditingID(null); setShowEditor(true); setError(null)
	}
	function beginEdit(entry: NetworkDeviceEntry) {
		setDraft({ ...entry }); setEditingID(String(entry.id ?? '')); setShowEditor(true); setError(null)
	}
	function cancelEditor() { setShowEditor(false); setEditingID(null); setError(null) }
	function applyDraft() {
		const normalized = normalizedEntry(draft, fallbackProbeMethod)
		if (!normalized.entry) { setError(normalized.error ?? '网络设备配置无效'); return }
		if (entries.some((item) => String(item.id ?? '') === normalized.entry!.id && String(item.id ?? '') !== editingID)) {
			setError(`设备 ID“${normalized.entry.id}”已经存在。`); return
		}
		const next = editingID
			? entries.map((item) => String(item.id ?? '') === editingID ? normalized.entry! : item)
			: [...entries, normalized.entry]
		replaceEntries(next); cancelEditor()
		setResult(editingID ? `已更新网络设备“${normalized.entry.name}”；保存后生效。` : `已将网络设备“${normalized.entry.name}”加入草稿；保存后生效。`)
	}
	async function save() {
		let parsed: unknown
		try { parsed = JSON.parse(entriesJSON) } catch { setError('网络设备配置必须是有效 JSON 数组。'); return }
		if (!Array.isArray(parsed)) { setError('网络设备配置必须是 JSON 数组。'); return }
		const normalized: NetworkDeviceEntry[] = []
		for (const item of parsed) {
			if (!item || typeof item !== 'object' || Array.isArray(item)) { setError('网络设备配置中的每一项都必须是对象。'); return }
			const next = normalizedEntry(item as NetworkDeviceEntry, fallbackProbeMethod)
			if (!next.entry) { setError(next.error ?? '网络设备配置无效'); return }
			if (normalized.some((entry) => String(entry.id) === String(next.entry!.id))) { setError(`设备 ID“${next.entry.id}”已经存在。`); return }
			normalized.push(next.entry)
		}
		setSaving(true); setError(null); setResult(null)
		try {
			await onSave({ id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled, config: { ...provider.config, devices: normalized } }, true)
			replaceEntries(normalized)
			setResult(`已保存 ${normalized.length} 台网络设备；Provider 已实时应用。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '保存网络设备失败') } finally { setSaving(false) }
	}
	const probeMethod = String(draft.probeMethod ?? fallbackProbeMethod ?? 'tcp')

	return <section className="xiaomi-device-manager network-device-manager">
		<header><div><p className="eyebrow">NETWORK · DEVICE CATALOG</p><h3>{provider.name} · 网络设备</h3><p>网络设备没有可选的厂商模型：每台设备固定映射为“网络设备 · 电源状态”，通过 TCP 或 ICMP 读取状态，配置 MAC 后可使用 Wake-on-LAN。</p></div><button type="button" onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? '网络设备 Provider 运行中' : '网络设备 Provider 未运行'}</strong><small>{provider.id} · 草稿 {entries.length} 台 · 已发布 {devices.length} 台</small></div><button type="button" disabled={!connected || showEditor} onClick={beginAdd}>手动添加设备</button></div>
		<ProviderDeviceAddFlow source="手动填写稳定 ID、设备名称、Host 与可选 MAC；网络设备没有云目录或扫描依赖。" model="模型固定为 network-device；探测方式可继承 Provider 默认值，或按设备覆盖。" configuration="先加入草稿；可改名或移出，最后统一点击“保存设备并应用”。" />
		{!connected && <p className="inline-error" role="alert">请先保存并启用网络设备 Provider；状态变为 running 后才能添加并实时应用设备。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success" role="status">{result}</p>}
		{showEditor && <div className="xiaomi-device-binding virtual-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>{editingID ? '编辑网络设备草稿' : '添加网络设备草稿'}</strong><small>保存后设备才会进入 Provider 运行目录；名称在此草稿中维护。</small></div></div><div className="form-grid">
			<label>设备 ID<input aria-label="网络设备 ID" disabled={Boolean(editingID)} value={String(draft.id ?? '')} onChange={(event) => updateDraft('id', event.target.value)} placeholder="living-room-pc" /></label>
			<label>来源名称<input aria-label="网络设备名称" value={String(draft.name ?? '')} onChange={(event) => updateDraft('name', event.target.value)} placeholder="客厅电脑" /></label>
			<label>Host<input aria-label="网络设备 Host" value={String(draft.host ?? '')} onChange={(event) => updateDraft('host', event.target.value)} placeholder="192.168.1.100" /></label>
			<label>探测方式<select aria-label="网络设备探测方式" value={probeMethod} onChange={(event) => updateDraft('probeMethod', event.target.value)}><option value="tcp">TCP 端口</option><option value="icmp">ICMP Ping</option></select></label>
			<label>TCP 探测端口<input aria-label="网络设备探测端口" type="number" min="1" max="65535" disabled={probeMethod === 'icmp'} value={draft.probePort === undefined ? '' : Number(draft.probePort)} onChange={(event) => updateDraft('probePort', numberOrUndefined(event.target.value))} placeholder="ICMP 不需要端口" /></label>
			<label>MAC（Wake-on-LAN，可选）<input aria-label="网络设备 MAC" value={String(draft.mac ?? '')} onChange={(event) => updateDraft('mac', event.target.value)} placeholder="AA:BB:CC:DD:EE:FF" /></label>
			</div><details><summary>单台设备高级覆盖（可选）</summary><div className="mqtt-tls-grid"><label>探测间隔（秒）<input aria-label="网络设备探测间隔覆盖" type="number" min="1" max="3600" value={draft.probeIntervalSeconds === undefined ? '' : Number(draft.probeIntervalSeconds)} onChange={(event) => updateDraft('probeIntervalSeconds', numberOrUndefined(event.target.value))} placeholder="继承 Provider" /></label><label>探测超时（秒）<input aria-label="网络设备探测超时覆盖" type="number" min="1" max="120" value={draft.probeTimeoutSeconds === undefined ? '' : Number(draft.probeTimeoutSeconds)} onChange={(event) => updateDraft('probeTimeoutSeconds', numberOrUndefined(event.target.value))} placeholder="继承 Provider" /></label><label>唤醒确认宽限（秒）<input aria-label="网络设备唤醒确认宽限覆盖" type="number" min="5" max="3600" value={draft.wakeGraceSeconds === undefined ? '' : Number(draft.wakeGraceSeconds)} onChange={(event) => updateDraft('wakeGraceSeconds', numberOrUndefined(event.target.value))} placeholder="继承 Provider" /></label><label>开启阈值<input aria-label="网络设备在线阈值覆盖" type="number" min="1" max="100" value={draft.onlineThreshold === undefined ? '' : Number(draft.onlineThreshold)} onChange={(event) => updateDraft('onlineThreshold', numberOrUndefined(event.target.value))} placeholder="继承 Provider" /></label><label>关闭阈值<input aria-label="网络设备离线阈值覆盖" type="number" min="1" max="100" value={draft.offlineThreshold === undefined ? '' : Number(draft.offlineThreshold)} onChange={(event) => updateDraft('offlineThreshold', numberOrUndefined(event.target.value))} placeholder="继承 Provider" /></label><label>WOL 广播地址<input aria-label="网络设备 WOL 广播地址覆盖" value={String(draft.wolBroadcastAddress ?? '')} onChange={(event) => updateDraft('wolBroadcastAddress', event.target.value)} placeholder="继承 Provider" /></label><label>WOL 端口<input aria-label="网络设备 WOL 端口覆盖" type="number" min="1" max="65535" value={draft.wolPort === undefined ? '' : Number(draft.wolPort)} onChange={(event) => updateDraft('wolPort', numberOrUndefined(event.target.value))} placeholder="继承 Provider" /></label><label className="wide">WOL 网络接口<input aria-label="网络设备 WOL 网络接口覆盖" value={String(draft.wolInterface ?? '')} onChange={(event) => updateDraft('wolInterface', event.target.value)} placeholder="继承 Provider" /></label></div></details><div className="form-actions"><button type="button" onClick={cancelEditor}>取消</button><button type="button" className="primary" onClick={applyDraft}>{editingID ? '更新草稿' : '加入草稿'}</button></div></div>}
		<div className="provider-device-list virtual-device-list"><div className="command-heading"><h3>设备草稿与已保存目录</h3><span>{entries.length} 台</span></div>{entries.length === 0 ? <p>还没有网络设备。点击“手动添加设备”填写来源连接信息。</p> : entries.map((entry) => { const current = published.get(String(entry.id)); return <div key={String(entry.id)}><span className={`status-dot ${current?.availability === 'online' ? 'is-online' : ''}`} /><strong>{String(current?.name || entry.name || entry.id)}</strong><code>{String(entry.id)}</code><small>{String(entry.host)} · {String(entry.probeMethod || fallbackProbeMethod).toUpperCase()}{String(entry.probeMethod || fallbackProbeMethod) === 'tcp' ? `:${Number(entry.probePort || 0)}` : ''}{entry.mac ? ' · Wake-on-LAN' : ' · 仅监测'} · {current ? (current.availability === 'online' ? '在线' : current.availability) : '等待发布'}</small><button type="button" onClick={() => beginEdit(entry)}>编辑</button><button type="button" className="is-danger" onClick={() => replaceEntries(entries.filter((item) => String(item.id) !== String(entry.id)))}>移除</button></div> })}</div>
		<details><summary>网络设备高级 JSON</summary><textarea aria-label="网络设备 JSON" rows={14} value={entriesJSON} onChange={(event) => setEntriesJSON(event.target.value)} spellCheck={false} /><small>用于批量导入或补充未展示的字段；日常添加请使用上方统一草稿流程。</small></details>
		<div className="form-actions"><button type="button" onClick={onClose}>取消</button><button type="button" className="primary" disabled={!connected || saving || showEditor} onClick={() => void save()}>{saving ? '应用中…' : '保存设备并应用'}</button></div>
	</section>
}
