import { useMemo, useState } from 'react'
import { scanProviderNetwork, type ProviderDiscoveryCandidate } from '../api/providers'
import { discoverSonoffDevices, type SonoffDirectoryDevice } from '../api/sonoff'
import { deviceTypeLabel } from '../presentationLabels'
import type { Device, DeviceType } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { ProviderDeviceAddFlow } from './ProviderDeviceAddFlow'

type SonoffEntry = Record<string, unknown>
type DirectoryRow = SonoffEntry & { deviceId: string; cloudAvailable?: boolean; lanAvailable?: boolean; configured?: boolean; encrypted?: boolean }

const sonoffDeviceTypes = [
	'switch', 'outlet', 'lightbulb', 'fan', 'temperature-sensor', 'humidity-sensor',
	'temperature-humidity-sensor', 'contact-sensor', 'motion-sensor', 'window-covering',
] as const satisfies readonly DeviceType[]

function supportedDeviceType(value: unknown): value is typeof sonoffDeviceTypes[number] {
	return typeof value === 'string' && (sonoffDeviceTypes as readonly string[]).includes(value)
}

function inferSonoffDeviceType(item: SonoffEntry): typeof sonoffDeviceTypes[number] {
	if (supportedDeviceType(item.type)) return item.type
	const uiid = Number(item.uiid || 0)
	if (uiid === 15 || uiid === 181) return 'temperature-humidity-sensor'
	if (uiid === 32 || uiid === 190) return 'outlet'
	if (uiid === 34) return 'fan'
	if (uiid === 36 || uiid === 77 || uiid === 136) return 'lightbulb'
	if (uiid === 102) return 'contact-sensor'
	if (uiid === 173 || uiid === 177) return 'motion-sensor'
	if (uiid === 126 && item.params && typeof item.params === 'object' && !Array.isArray(item.params) && 'motorTurn' in item.params) return 'window-covering'
	return 'switch'
}

function configuredEntries(provider: Provider): SonoffEntry[] {
	return Array.isArray(provider.config.devices)
		? provider.config.devices.filter((item): item is SonoffEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item))).map((item) => ({ ...item, type: inferSonoffDeviceType(item) }))
		: []
}

function stableSonoffID(deviceID: string): string {
	const stable = deviceID.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || 'device'
	return `sonoff-${stable}`
}

function lanDeviceID(candidate: ProviderDiscoveryCandidate): string {
	return String(candidate.metadata?.deviceId || candidate.id || '').trim().replace(/^sonoff-/, '')
}

function candidateEntry(row: DirectoryRow, deviceType: DeviceType): SonoffEntry {
	return {
		id: String(row.id || stableSonoffID(row.deviceId)), deviceId: row.deviceId,
		name: String(row.name || row.deviceId), model: String(row.model || ''), uiid: Number(row.uiid || 0), type: deviceType,
		homeId: String(row.homeId || ''), homeName: String(row.homeName || ''), roomId: String(row.roomId || ''), roomName: String(row.roomName || ''),
		host: String(row.host || ''), port: Number(row.port || 8081), channels: Math.max(1, Number(row.channels || 1)), diy: row.diy === true,
	}
}

export function SonoffDeviceManager({ provider, devices, onClose, onSave }: {
	provider: Provider
	devices: Device[]
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
}) {
	const initialEntries = useMemo(() => configuredEntries(provider), [provider])
	const [entries, setEntries] = useState(initialEntries)
	const [cloudDevices, setCloudDevices] = useState<SonoffDirectoryDevice[]>([])
	const [lanCandidates, setLANCandidates] = useState<ProviderDiscoveryCandidate[]>([])
	const [deviceTypes, setDeviceTypes] = useState<Record<string, DeviceType>>(() => Object.fromEntries(initialEntries.map((item) => [String(item.deviceId || ''), inferSonoffDeviceType(item)]).filter(([deviceID]) => deviceID)))
	const [deviceNames, setDeviceNames] = useState<Record<string, string>>(() => Object.fromEntries(initialEntries.map((item) => [String(item.deviceId || ''), String(item.name || '')]).filter(([deviceID]) => deviceID)))
	const [cloudLoading, setCloudLoading] = useState(false)
	const [lanLoading, setLANLoading] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const connected = provider.enabled && provider.status === 'running'
	const published = useMemo(() => new Set(devices.map((item) => item.id)), [devices])
	const managedIDs = useMemo(() => new Set(entries.map((item) => String(item.deviceId || '')).filter(Boolean)), [entries])
	const directory = useMemo(() => {
		const rows = new Map<string, DirectoryRow>()
		for (const item of cloudDevices) rows.set(item.deviceId, { ...item, cloudAvailable: true })
		for (const candidate of lanCandidates) {
			const deviceId = lanDeviceID(candidate)
			if (!deviceId) continue
			const current = rows.get(deviceId) ?? { deviceId }
			rows.set(deviceId, { ...current, name: current.name || candidate.name, id: current.id || candidate.id || stableSonoffID(deviceId), host: candidate.host, port: candidate.port || 8081, type: current.type || candidate.metadata?.type || '', diy: candidate.metadata?.diy === 'true', encrypted: candidate.metadata?.encrypted === 'true', lanAvailable: true })
		}
		for (const entry of entries) {
			const deviceId = String(entry.deviceId || '').trim()
			if (!deviceId) continue
			rows.set(deviceId, { ...(rows.get(deviceId) ?? { deviceId }), ...entry, deviceId, configured: true })
		}
		return [...rows.values()].sort((a, b) => String(a.name || a.deviceId).localeCompare(String(b.name || b.deviceId), 'zh-CN'))
	}, [cloudDevices, lanCandidates, entries])

	async function refreshCloud() {
		setCloudLoading(true); setError(null); setResult(null)
		try {
			const items = await discoverSonoffDevices(provider.id)
			setCloudDevices(items)
			setResult(items.length ? `eWeLink 云目录返回 ${items.length} 台设备；请选择设备并保存到受管清单。` : 'eWeLink 云目录暂时为空；已保存设备仍会保留。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取 eWeLink 设备目录') } finally { setCloudLoading(false) }
	}

	async function scanLAN() {
		setLANLoading(true); setError(null); setResult(null)
		try {
			const items = await scanProviderNetwork({ id: provider.id, name: provider.name, type: 'sonoff', enabled: false, config: { ...provider.config, devices: entries } })
			setLANCandidates(items)
			setResult(items.length ? `局域网发现 ${items.length} 台设备；请加入受管清单并保存。` : '局域网扫描未发现设备；已保存设备不会被删除。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : 'Sonoff 局域网扫描失败') } finally { setLANLoading(false) }
	}

	function add(row: DirectoryRow) {
		if (managedIDs.has(row.deviceId)) return
		const name = String(deviceNames[row.deviceId] ?? row.name ?? '').trim() || row.deviceId
		setEntries((current) => [...current, candidateEntry({ ...row, name }, deviceTypes[row.deviceId] ?? inferSonoffDeviceType(row))])
		setResult(`已将“${name}”加入草稿；点击保存后才会持续发布。`)
	}

	function updateDeviceName(row: DirectoryRow, value: string) {
		setDeviceNames((current) => ({ ...current, [row.deviceId]: value }))
		if (managedIDs.has(row.deviceId)) {
			setEntries((current) => current.map((item) => String(item.deviceId || '') === row.deviceId ? { ...item, name: value } : item))
		}
	}

	function updateDeviceType(row: DirectoryRow, value: DeviceType) {
		setDeviceTypes((current) => ({ ...current, [row.deviceId]: value }))
		if (managedIDs.has(row.deviceId)) {
			setEntries((current) => current.map((item) => String(item.deviceId || '') === row.deviceId ? { ...item, type: value } : item))
		}
	}

	function remove(deviceID: string) {
		setEntries((current) => current.filter((item) => String(item.deviceId || '') !== deviceID))
	}

	async function save() {
		setSaving(true); setError(null); setResult(null)
		try {
			const normalizedEntries = entries.map((item) => ({ ...item, name: String(item.name || '').trim() || String(item.deviceId || '') }))
			await onSave({ id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled, config: { ...provider.config, managedDevices: true, devices: normalizedEntries } }, true)
			setEntries(normalizedEntries)
			setResult(`已保存 ${normalizedEntries.length} 台易微联设备；云端暂时漏报时仍会在发布者下保留为离线。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '保存易微联设备失败') } finally { setSaving(false) }
	}

	return <section className="xiaomi-device-manager">
		<header><div><p className="eyebrow">SONOFF · EWELINK DEVICES</p><h3>{provider.name} · 易微联设备</h3><p>云目录、局域网扫描和已保存清单在这里合并。扫描结果只是候选，加入并保存后才会稳定出现在发布者下面。</p></div><button onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? 'eWeLink Provider 已连接' : 'eWeLink Provider 未运行'}</strong><small>{provider.id} · 已管理 {entries.length} 台 · 已发布 {devices.length} 台</small></div><button disabled={!connected || cloudLoading} onClick={() => void refreshCloud()}>{cloudLoading ? '正在读取…' : cloudDevices.length ? '刷新 eWeLink 设备目录' : '读取 eWeLink 设备目录'}</button><button disabled={!connected || lanLoading} onClick={() => void scanLAN()}>{lanLoading ? '正在扫描…' : '扫描 Sonoff 局域网'}</button></div>
		<ProviderDeviceAddFlow source="从 eWeLink 云目录或 Sonoff 局域网扫描读取候选设备。" model="为每台候选设备选择统一模型，并在草稿中填写设备名称。" configuration="候选设备先加入草稿；可改名或移出，最后统一点击“保存设备并应用”。" />
		{!connected && <p className="inline-error" role="alert">请先登录 eWeLink、保存并启用 Provider；状态变为 running 后再管理设备。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success">{result}</p>}
		<div className="xiaomi-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>易微联设备目录</strong><small>已保存设备始终显示；云端或局域网短暂未发现不会自动移除。</small></div><span>{directory.length} 台</span></div>
			{directory.length === 0 ? <p className="xiaomi-device-binding__empty">读取云目录或扫描局域网后选择设备。若之前已经保存过设备，它们会直接显示在这里。</p> : <div className="xiaomi-hub-device-list">{directory.map((row) => {
				const managed = managedIDs.has(row.deviceId)
				const id = String(row.id || stableSonoffID(row.deviceId))
				const selectedType = deviceTypes[row.deviceId] ?? inferSonoffDeviceType(row)
				const editableName = deviceNames[row.deviceId] ?? String(row.name || '')
				return <article key={row.deviceId}><div><strong>{editableName.trim() || row.deviceId}</strong><small>{String(row.homeName || '未知家庭')} / {String(row.roomName || '未分配房间')} · {String(row.model || '型号未知')} · UIID {Number(row.uiid || 0)}</small><span className="xiaomi-route-capabilities"><i className={row.cloudAvailable ? 'is-ready' : ''}>{row.cloudAvailable ? 'eWeLink 云可见' : '云端暂未返回'}</i><i className={row.lanAvailable || row.host ? 'is-ready' : ''}>{row.lanAvailable || row.host ? `局域网 ${String(row.host || '已配置')}` : '未发现局域网地址'}</i><i className={published.has(id) ? 'is-ready' : ''}>{published.has(id) ? '发布者已发布' : managed ? '已保存，等待刷新' : '尚未管理'}</i></span><code>{row.deviceId}</code>{row.encrypted && !row.cloudAvailable && <small>该局域网设备需要从 eWeLink 云目录取得 devicekey 后才能本地加密控制。</small>}</div><label className="sonoff-device-name">设备名称<input aria-label={`设备 ${row.deviceId} 名称`} value={editableName} placeholder={row.deviceId} maxLength={128} onChange={(event) => updateDeviceName(row, event.target.value)} /></label><label>统一模型（deviceType）<select aria-label={`${editableName.trim() || row.deviceId} 统一模型`} value={selectedType} onChange={(event) => updateDeviceType(row, event.target.value as DeviceType)}>{sonoffDeviceTypes.map((type) => <option value={type} key={type}>{deviceTypeLabel(type)}</option>)}</select></label>{managed ? <button type="button" className="is-danger" onClick={() => remove(row.deviceId)}>移出草稿</button> : <button type="button" onClick={() => add(row)}>加入草稿</button>}</article>
			})}</div>}
			<div className="xiaomi-mapped-summary"><strong>已管理 {entries.length} 台设备</strong><small>保存后配置写入数据库；后续云端目录为空、网络波动或服务重启时，设备仍以原内部 ID 保留，不会从发布者下消失。</small></div>
		</div>
		<details><summary>受管设备高级 JSON</summary><textarea aria-label="Sonoff 受管设备 JSON" rows={14} value={JSON.stringify(entries, null, 2)} readOnly /><small>设备密钥由后端加密保存并脱敏显示；云目录接口不会返回 devicekey 或原始 params。</small></details>
		<div className="form-actions"><button onClick={onClose}>取消</button><button className="primary" disabled={!connected || saving} onClick={() => void save()}>{saving ? '正在应用…' : '保存设备并应用'}</button></div>
	</section>
}
