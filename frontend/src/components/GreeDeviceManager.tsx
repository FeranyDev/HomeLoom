import { useMemo, useState } from 'react'
import { scanProviderNetwork, type ProviderDiscoveryCandidate } from '../api/providers'
import type { Device } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { ProviderDeviceAddFlow } from './ProviderDeviceAddFlow'

type GreeDeviceEntry = Record<string, unknown>

const deviceIDPattern = /^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/

function configuredDevices(provider: Provider): GreeDeviceEntry[] {
	return Array.isArray(provider.config.devices)
		? provider.config.devices.filter((item): item is GreeDeviceEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item))).map((item) => ({ ...item }))
		: []
}

function normalizeMAC(value: unknown): string {
	return String(value ?? '').trim().toLowerCase().split('@').map((part) => part.replace(/[:\-\s]/g, '')).join('@')
}

function candidateKey(candidate: Pick<ProviderDiscoveryCandidate, 'mac'>): string {
	return normalizeMAC(candidate.mac)
}

function entryKey(entry: GreeDeviceEntry): string {
	return normalizeMAC(entry.mac)
}

function dedupeCandidates(items: ProviderDiscoveryCandidate[]): ProviderDiscoveryCandidate[] {
	const seen = new Set<string>()
	return items.filter((item) => {
		const key = candidateKey(item)
		if (!key || seen.has(key)) return false
		seen.add(key)
		return true
	})
}

function defaultDraft(index: number): GreeDeviceEntry {
	return { id: `gree-device-${index}`, name: '', host: '', port: 7000, mac: '', encryptionKey: '', encryptionVersion: '', uid: '', targetTemperatureStep: 1, tempSensorOffset: '', disableAvailableCheck: false, autoXFan: false, autoLight: false, enabled: true }
}

function draftFromEntry(entry: GreeDeviceEntry): GreeDeviceEntry {
	return { ...defaultDraft(1), ...entry, encryptionVersion: entry.encryptionVersion ?? '', uid: entry.uid ?? '' }
}

function normalizeEntry(draft: GreeDeviceEntry): { entry?: GreeDeviceEntry; error?: string } {
	const id = String(draft.id ?? '').trim().toLowerCase()
	const name = String(draft.name ?? '').trim()
	const host = String(draft.host ?? '').trim()
	const mac = normalizeMAC(draft.mac)
	const port = Number(draft.port ?? 7000)
	if (!deviceIDPattern.test(id)) return { error: '设备 ID 必须是 1–64 位小写稳定标识，可使用数字、点、下划线和连字符。' }
	if (!name) return { error: '请输入格力设备名称。' }
	if (!host) return { error: '请输入格力设备地址（host）。' }
	if (!mac || !/^[0-9a-f]{12}(?:@[0-9a-f]{12})?$/.test(mac)) return { error: '请输入有效的格力设备 MAC 地址。' }
	if (!Number.isInteger(port) || port < 1 || port > 65535) return { error: '设备端口必须是 1–65535 的整数。' }
	const version = draft.encryptionVersion === '' || draft.encryptionVersion === undefined || draft.encryptionVersion === null ? undefined : Number(draft.encryptionVersion)
	if (version !== undefined && version !== 1 && version !== 2) return { error: '加密版本仅支持 v1 或 v2。' }
	const targetTemperatureStep = Number(draft.targetTemperatureStep ?? 1)
	if (!Number.isFinite(targetTemperatureStep) || targetTemperatureStep < 0.1 || targetTemperatureStep > 5) return { error: '目标温度步长必须是 0.1–5。' }
	const uidText = String(draft.uid ?? '').trim()
	if (uidText && (!/^\d+$/.test(uidText) || Number(uidText) < 0)) return { error: 'UID 必须是非负整数。' }
	const entry: GreeDeviceEntry = { id, name, host, port, mac, enabled: draft.enabled !== false, targetTemperatureStep }
	const key = String(draft.encryptionKey ?? '').trim()
	if (key && key !== '********') entry.encryptionKey = key
	if (version !== undefined) entry.encryptionVersion = version
	if (uidText) entry.uid = Number(uidText)
	if (draft.tempSensorOffset === true || draft.tempSensorOffset === false) entry.tempSensorOffset = draft.tempSensorOffset
	if (draft.disableAvailableCheck === true) entry.disableAvailableCheck = true
	if (draft.autoXFan === true) entry.autoXFan = true
	if (draft.autoLight === true) entry.autoLight = true
	return { entry }
}

export function GreeDeviceManager({ provider, devices, onClose, onSave }: {
	provider: Provider
	devices: Device[]
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
}) {
	const initialEntries = useMemo(() => configuredDevices(provider), [provider])
	const [entries, setEntries] = useState(initialEntries)
	const [entriesJSON, setEntriesJSON] = useState(JSON.stringify(initialEntries, null, 2))
	const [draft, setDraft] = useState<GreeDeviceEntry>(() => defaultDraft(initialEntries.length + 1))
	const [editingID, setEditingID] = useState<string | null>(null)
	const [showEditor, setShowEditor] = useState(false)
	const [discovering, setDiscovering] = useState(false)
	const [candidates, setCandidates] = useState<ProviderDiscoveryCandidate[]>([])
	const [scanAttempted, setScanAttempted] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const connected = provider.enabled && provider.status === 'running'
	const published = useMemo(() => new Map(devices.map((item) => [item.id, item])), [devices])

	function replaceEntries(next: GreeDeviceEntry[]) {
		setEntries(next)
		setEntriesJSON(JSON.stringify(next, null, 2))
	}

	function updateDraft(field: string, value: unknown) {
		setDraft((current) => ({ ...current, [field]: value }))
	}

	function beginAdd() {
		setDraft(defaultDraft(entries.length + 1))
		setEditingID(null)
		setShowEditor(true)
		setError(null)
	}

	function beginEdit(entry: GreeDeviceEntry) {
		setDraft(draftFromEntry(entry))
		setEditingID(String(entry.id ?? ''))
		setShowEditor(true)
		setError(null)
	}

	function cancelEditor() {
		setShowEditor(false)
		setEditingID(null)
		setError(null)
	}

	function applyDraft() {
		const normalized = normalizeEntry(draft)
		if (!normalized.entry) {
			setError(normalized.error ?? '格力设备配置无效')
			return
		}
		if (entries.some((item) => String(item.id ?? '') === normalized.entry!.id && String(item.id ?? '') !== editingID)) {
			setError(`设备 ID“${normalized.entry.id}”已经存在。`)
			return
		}
		if (entries.some((item) => entryKey(item) === normalized.entry!.mac && String(item.id ?? '') !== editingID)) {
			setError('该 MAC 地址已经添加到当前 Provider。')
			return
		}
		const next = editingID ? entries.map((item) => String(item.id ?? '') === editingID ? normalized.entry! : item) : [...entries, normalized.entry]
		replaceEntries(next)
		cancelEditor()
		setResult(editingID ? `已更新格力设备“${normalized.entry.name}”。` : `已添加格力设备“${normalized.entry.name}”。`)
	}

	async function scan() {
		setDiscovering(true); setScanAttempted(true); setError(null); setResult(null)
		try {
			const found = await scanProviderNetwork({ id: `${provider.id}-scan`, name: `${provider.name} 局域网扫描`, type: 'gree', enabled: false, config: { ...provider.config, devices: [] } })
			setCandidates(dedupeCandidates(found))
		} catch (cause) {
			setCandidates([])
			setError(cause instanceof Error ? cause.message : 'Gree 局域网扫描失败')
		} finally { setDiscovering(false) }
	}

	function addCandidate(candidate: ProviderDiscoveryCandidate) {
		const key = candidateKey(candidate)
		if (!key || entries.some((item) => entryKey(item) === key)) return
		const entry: GreeDeviceEntry = { id: candidate.id || `gree-${key.replace('@', '-')}`, name: candidate.name || '格力空调', host: candidate.host, port: candidate.port || 7000, mac: key, enabled: true }
		replaceEntries([...entries, entry])
		setResult(`已加入“${entry.name}”，点击保存后应用。`)
	}

	async function save() {
		let parsed: unknown
		try { parsed = JSON.parse(entriesJSON) } catch { setError('格力设备配置必须是有效 JSON'); return }
		if (!Array.isArray(parsed)) { setError('格力设备配置必须是 JSON 数组'); return }
		const sanitized = parsed.map((item) => {
			if (!item || typeof item !== 'object' || Array.isArray(item)) return item
			const copy = { ...(item as GreeDeviceEntry) }
			if (copy.encryptionKey === '********') delete copy.encryptionKey
			return copy
		})
		setSaving(true); setError(null); setResult(null)
		try {
			await onSave({ id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled, config: { ...provider.config, devices: sanitized } }, true)
			const next = sanitized.filter((item): item is GreeDeviceEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item))).map((item) => ({ ...item }))
			setEntries(next); setEntriesJSON(JSON.stringify(next, null, 2)); setResult(`已保存 ${next.length} 台格力设备；Provider 运行目录已实时应用。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '保存格力设备失败') } finally { setSaving(false) }
	}

	return <section className="xiaomi-device-manager gree-device-manager">
		<header><div><p className="eyebrow">GREE · LAN DEVICES</p><h3>{provider.name} · 格力设备</h3><p>每台空调独立配置 host、MAC、加密版本和密钥；保存后会通过 Provider live reconfigure 实时应用。</p></div><button type="button" onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? 'Gree Provider 运行中' : 'Gree Provider 未运行'}</strong><small>{provider.id} · {entries.length} 台已配置设备</small></div><button type="button" disabled={!connected || discovering || showEditor} onClick={() => void scan()}>{discovering ? '正在扫描局域网…' : '扫描局域网设备'}</button><button type="button" disabled={!connected || showEditor} onClick={beginAdd}>手动添加</button></div>
		<ProviderDeviceAddFlow source="扫描格力局域网设备，或手动填写空调连接信息。" model="格力设备固定映射为空调模型；确认加密方式和来源连接字段。" configuration="设备先加入草稿，最后统一点击“保存设备并应用”。" />
		{!connected && <p className="inline-error" role="alert">请先启用 Gree Provider；状态变为 running 后才能扫描或添加设备。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success" role="status">{result}</p>}
		{scanAttempted && !discovering && !error && candidates.length === 0 && <p className="xiaomi-device-binding__empty">未发现 Gree 设备。请确认 HomeLoom 与空调在同一局域网，且 UDP/7000 未被隔离。</p>}
		{candidates.length > 0 && <div className="xiaomi-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>局域网发现结果</strong><small>发现结果只填入设备连接信息，密钥仍由你确认。</small></div><span>{candidates.length} 台</span></div><div className="xiaomi-hub-device-list">{candidates.map((candidate) => { const added = entries.some((item) => entryKey(item) === candidateKey(candidate)); return <article key={`${candidateKey(candidate)}-${candidate.host}`}><div><strong>{candidate.name || '格力空调'}</strong><small>{candidate.host}:{candidate.port || 7000}</small><code>{candidate.mac}</code></div><button type="button" disabled={added} onClick={() => addCandidate(candidate)}>{added ? '已加入草稿' : '加入草稿'}</button></article> })}</div></div>}
		{showEditor && <div className="xiaomi-device-binding virtual-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>{editingID ? '编辑格力设备草稿' : '添加格力设备草稿'}</strong><small>设备 ID 保存后不可修改；密码字段留空表示不覆盖已有密钥。</small></div></div><div className="form-grid"><label>设备 ID<input aria-label="格力设备 ID" disabled={Boolean(editingID)} value={String(draft.id ?? '')} onChange={(event) => updateDraft('id', event.target.value)} /></label><label>名称<input aria-label="格力设备名称" value={String(draft.name ?? '')} onChange={(event) => updateDraft('name', event.target.value)} placeholder="客厅格力空调" /></label><label>设备地址（host）<input aria-label="格力设备地址" value={String(draft.host ?? '')} onChange={(event) => updateDraft('host', event.target.value)} placeholder="192.168.1.42" /></label><label>端口（port）<input aria-label="格力设备端口" type="number" min="1" max="65535" value={String(draft.port ?? 7000)} onChange={(event) => updateDraft('port', event.target.value === '' ? '' : Number(event.target.value))} /></label><label>MAC 地址<input aria-label="格力设备 MAC" value={String(draft.mac ?? '')} onChange={(event) => updateDraft('mac', event.target.value)} placeholder="AA:BB:CC:DD:EE:FF" /></label><label>加密版本<select aria-label="格力加密版本" value={String(draft.encryptionVersion ?? '')} onChange={(event) => updateDraft('encryptionVersion', event.target.value)}><option value="">默认 v1</option><option value="1">v1</option><option value="2">v2</option></select></label><label>加密密钥<input aria-label="格力加密密钥" type="password" value={String(draft.encryptionKey ?? '')} onChange={(event) => updateDraft('encryptionKey', event.target.value)} placeholder="可选" /></label><label>UID（可选）<input aria-label="格力设备 UID" inputMode="numeric" value={String(draft.uid ?? '')} onChange={(event) => updateDraft('uid', event.target.value)} /></label><label>目标温度步长<input aria-label="格力目标温度步长" type="number" min="0.1" max="5" step="0.1" value={String(draft.targetTemperatureStep ?? 1)} onChange={(event) => updateDraft('targetTemperatureStep', event.target.value === '' ? '' : Number(event.target.value))} /></label><label>温度传感器偏移<select aria-label="格力温度传感器偏移" value={draft.tempSensorOffset === true ? 'true' : draft.tempSensorOffset === false ? 'false' : ''} onChange={(event) => updateDraft('tempSensorOffset', event.target.value === '' ? undefined : event.target.value === 'true')}><option value="">自动判断</option><option value="true">固定减 40</option><option value="false">不减 40</option></select></label><label className="enable-row"><input aria-label="格力禁用可用性检查" type="checkbox" checked={draft.disableAvailableCheck === true} onChange={(event) => updateDraft('disableAvailableCheck', event.target.checked)} />始终保持可用</label><label className="enable-row"><input aria-label="格力自动 X-Fan" type="checkbox" checked={draft.autoXFan === true} onChange={(event) => updateDraft('autoXFan', event.target.checked)} />自动 X-Fan</label><label className="enable-row"><input aria-label="格力自动面板灯" type="checkbox" checked={draft.autoLight === true} onChange={(event) => updateDraft('autoLight', event.target.checked)} />自动面板灯</label><label className="enable-row"><input aria-label="启用格力设备" type="checkbox" checked={draft.enabled !== false} onChange={(event) => updateDraft('enabled', event.target.checked)} />启用此设备</label></div><div className="form-actions"><button type="button" onClick={cancelEditor}>取消</button><button type="button" className="primary" onClick={applyDraft}>{editingID ? '更新草稿' : '加入草稿'}</button></div></div>}
		<div className="provider-device-list virtual-device-list"><div className="command-heading"><h3>已配置格力设备</h3><span>{entries.length} 台</span></div>{entries.length === 0 ? <p>尚未添加格力设备。点击“扫描局域网设备”或“手动添加”。</p> : entries.map((entry) => { const current = published.get(String(entry.id)); return <div key={String(entry.id)}><span className={`status-dot ${current?.availability === 'online' ? 'is-online' : ''}`} /><strong>{String(current?.name || entry.name || entry.id)}</strong><code>{String(entry.id)}</code><small>{String(entry.host)}:{Number(entry.port || 7000)} · MAC {String(entry.mac)} · {current ? (current.availability === 'online' ? '在线' : current.availability) : '等待运行时发布'}</small><button type="button" onClick={() => beginEdit(entry)}>编辑</button><button type="button" className="is-danger" onClick={() => replaceEntries(entries.filter((item) => String(item.id) !== String(entry.id)))}>移除</button></div> })}</div>
		<details><summary>格力设备高级 JSON</summary><textarea aria-label="格力设备 JSON" rows={14} value={entriesJSON} onChange={(event) => setEntriesJSON(event.target.value)} spellCheck={false} /><small>用于导入历史配置或补充 homeId、roomName 等未在表单展示的字段；保存时以后端严格校验结果为准。</small></details>
		<div className="form-actions"><button type="button" onClick={onClose}>取消</button><button type="button" className="primary" disabled={!connected || saving || showEditor} onClick={() => void save()}>{saving ? '应用中…' : '保存设备并应用'}</button></div>
	</section>
}
