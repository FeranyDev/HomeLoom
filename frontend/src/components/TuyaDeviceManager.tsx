import { useMemo, useState } from 'react'
import { discoverTuyaDevices, type TuyaDirectoryDevice } from '../api/tuya'
import { deviceTypeLabel } from '../presentationLabels'
import { builtInDeviceTypes, type Device, type DeviceType } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { ProviderDeviceAddFlow } from './ProviderDeviceAddFlow'

type TuyaEntry = Record<string, unknown>
type DirectoryRow = TuyaDirectoryDevice & TuyaEntry

function configuredEntries(provider: Provider): TuyaEntry[] {
	return Array.isArray(provider.config.devices)
		? provider.config.devices.filter((item): item is TuyaEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item))).map((item) => ({ ...item }))
		: []
}

function candidateEntry(row: DirectoryRow, type: DeviceType): TuyaEntry {
	return {
		id: row.id,
		deviceId: row.deviceId,
		name: row.name || row.deviceId,
		type,
		category: row.category || '',
		productId: row.productId || '',
		productName: row.productName || '',
		model: row.model || '',
		homeId: row.homeId || '',
		homeName: row.homeName || '',
		roomId: row.roomId || '',
		roomName: row.roomName || '',
		specification: row.specification ?? {},
		status: row.status ?? [],
	}
}

export function TuyaDeviceManager({ provider, devices, onClose, onSave }: {
	provider: Provider
	devices: Device[]
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
}) {
	const initialEntries = useMemo(() => configuredEntries(provider), [provider])
	const [entries, setEntries] = useState(initialEntries)
	const [cloudDevices, setCloudDevices] = useState<TuyaDirectoryDevice[]>([])
	const [loading, setLoading] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const connected = provider.enabled && provider.status === 'running'
	const managedIDs = useMemo(() => new Set(entries.map((item) => String(item.deviceId || '')).filter(Boolean)), [entries])
	const published = useMemo(() => new Set(devices.map((item) => item.id)), [devices])
	const directory = useMemo(() => {
		const rows = new Map<string, DirectoryRow>()
		for (const item of cloudDevices) rows.set(item.deviceId, { ...item })
		for (const entry of entries) {
			const deviceId = String(entry.deviceId || '').trim()
			if (!deviceId) continue
			const previous = rows.get(deviceId)
			rows.set(deviceId, {
				...previous, ...entry,
				id: String(entry.id || previous?.id || ''), deviceId, name: String(entry.name || previous?.name || deviceId),
				type: String(entry.type || previous?.type || 'switch'), online: previous?.online ?? false, configured: true,
				specification: previous?.specification ?? entry.specification as Record<string, unknown> | undefined,
				status: previous?.status ?? entry.status as Array<Record<string, unknown>> | undefined,
			} as DirectoryRow)
		}
		return [...rows.values()].sort((left, right) => String(left.name || left.deviceId).localeCompare(String(right.name || right.deviceId), 'zh-CN'))
	}, [cloudDevices, entries])

	async function refreshDirectory() {
		setLoading(true); setError(null); setResult(null)
		try {
			const items = await discoverTuyaDevices(provider.id)
			setCloudDevices(items)
			setResult(items.length ? `Tuya 云目录返回 ${items.length} 台设备；选择后保存到受管清单。` : 'Tuya 云目录暂时为空；已保存设备仍会保留。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取 Tuya 设备目录') } finally { setLoading(false) }
	}

	function updateType(row: DirectoryRow, type: DeviceType) {
		if (managedIDs.has(row.deviceId)) {
			setEntries((current) => current.map((item) => String(item.deviceId || '') === row.deviceId ? { ...item, type } : item))
		} else {
			setCloudDevices((current) => current.map((item) => item.deviceId === row.deviceId ? { ...item, type } : item))
		}
	}

	function add(row: DirectoryRow) {
		if (managedIDs.has(row.deviceId)) return
		setEntries((current) => [...current, candidateEntry(row, row.type as DeviceType)])
		setResult(`已将“${row.name || row.deviceId}”加入草稿；保存后才会持续发布。`)
	}

	function remove(deviceID: string) {
		setEntries((current) => current.filter((item) => String(item.deviceId || '') !== deviceID))
	}

	function updateEntryName(deviceID: string, name: string) {
		setEntries((current) => current.map((item) => String(item.deviceId || '') === deviceID ? { ...item, name } : item))
	}

	async function save() {
		setSaving(true); setError(null); setResult(null)
		try {
			const normalizedEntries = entries.map((entry) => ({ ...entry, name: String(entry.name ?? '').trim() || String(entry.deviceId || '') }))
			await onSave({ id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled, config: { ...provider.config, managedDevices: true, devices: normalizedEntries } }, true)
			setEntries(normalizedEntries)
			setResult(`已保存 ${normalizedEntries.length} 台 Tuya 设备；云目录短暂漏报时仍会保留为离线设备。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '保存 Tuya 设备失败') } finally { setSaving(false) }
	}

	return <section className="xiaomi-device-manager">
		<header><div><p className="eyebrow">TUYA · MANAGED DEVICES</p><h3>{provider.name} · 涂鸦设备</h3><p>账号目录只是候选来源。加入受管清单并保存后，原始 Tuya Device ID、规格和设备类型会持久化。</p></div><button onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? 'Tuya Provider 已连接' : 'Tuya Provider 未运行'}</strong><small>{provider.id} · 已管理 {entries.length} 台 · 已发布 {devices.length} 台</small></div><button disabled={!connected || loading} onClick={() => void refreshDirectory()}>{loading ? '正在读取…' : cloudDevices.length ? '刷新 Tuya 设备目录' : '读取 Tuya 设备目录'}</button></div>
		<ProviderDeviceAddFlow source="从 Tuya 云账号目录读取候选设备。" model="选择统一模型；加入草稿后可直接修改设备名称。" configuration="候选设备先加入草稿；可改名或移出，最后统一点击“保存设备并应用”。" />
		{!connected && <p className="inline-error" role="alert">请先完成 Tuya 扫码登录、保存并启用 Provider。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success">{result}</p>}
		<div className="xiaomi-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>Tuya 账号设备目录</strong><small>已保存设备始终显示；云端临时缺失时不会自动从发布者移除。</small></div><span>{directory.length} 台</span></div>
			{directory.length === 0 ? <p className="xiaomi-device-binding__empty">点击“读取 Tuya 设备目录”后选择设备。</p> : <div className="xiaomi-hub-device-list">{directory.map((row) => {
				const managed = managedIDs.has(row.deviceId)
				const selectedType = String(row.type || 'switch') as DeviceType
				return <article key={row.deviceId}><div><strong>{String(row.name || row.deviceId)}</strong><small>{String(row.homeName || '未知家庭')} / {String(row.roomName || '未分配房间')} · {String(row.productName || row.model || row.category || '型号未知')}</small><span className="xiaomi-route-capabilities"><i className={row.online ? 'is-ready' : ''}>{row.online ? 'Tuya 云在线' : '当前离线'}</i><i className={published.has(String(row.id)) ? 'is-ready' : ''}>{published.has(String(row.id)) ? '发布者已发布' : managed ? '已保存，等待刷新' : '尚未管理'}</i></span><code>{row.deviceId}</code></div><label>统一模型（deviceType）<select aria-label={`${String(row.name || row.deviceId)} 统一模型`} value={selectedType} onChange={(event) => updateType(row, event.target.value as DeviceType)}>{!(builtInDeviceTypes as readonly string[]).includes(selectedType) && <option value={selectedType}>{deviceTypeLabel(selectedType)}</option>}{builtInDeviceTypes.map((type) => <option value={type} key={type}>{deviceTypeLabel(type)}</option>)}</select></label>{managed ? <button type="button" className="is-danger" onClick={() => remove(row.deviceId)}>移出草稿</button> : <button type="button" onClick={() => add(row)}>加入草稿</button>}</article>
			})}</div>}
			<div className="provider-device-list virtual-device-list"><div className="command-heading"><h3>设备草稿与已保存目录</h3><span>{entries.length} 台</span></div>{entries.length === 0 ? <p>从上方目录加入设备后，可在这里修改名称或移出草稿，无需再次读取云端。</p> : entries.map((entry) => { const deviceID = String(entry.deviceId || ''); const name = String(entry.name ?? deviceID); return <article key={deviceID}><div><strong>{name || deviceID}</strong><code>{String(entry.id || deviceID)}</code></div><label>设备名称<input aria-label={`草稿设备 ${deviceID} 名称`} value={name} maxLength={128} onChange={(event) => updateEntryName(deviceID, event.target.value)} /></label><button type="button" className="is-danger" onClick={() => remove(deviceID)}>移出草稿</button></article> })}</div>
			<div className="xiaomi-mapped-summary"><strong>已管理 {entries.length} 台设备</strong><small>保存后只发布受管清单中的设备；未选择的账号设备不会进入 HomeLoom。</small></div>
		</div>
		<details><summary>受管设备高级 JSON</summary><textarea aria-label="Tuya 受管设备 JSON" rows={14} value={JSON.stringify(entries, null, 2)} readOnly /></details>
		<div className="form-actions"><button onClick={onClose}>取消</button><button className="primary" disabled={!connected || saving} onClick={() => void save()}>{saving ? '正在应用…' : '保存设备并应用'}</button></div>
	</section>
}
