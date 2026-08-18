import { useMemo, useState } from 'react'
import { setDeviceLocation } from '../api/devices'
import { discoverXiaomiDevices, type XiaomiHubDevice } from '../api/xiaomi'
import type { Device, DeviceLocationHome, DeviceLocationMode, DeviceType } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { defaultXiaomiMedia, inferXiaomiDeviceType, requiredXiaomiProperties, stableXiaomiControlID, stableXiaomiID, xiaomiDeviceTypes } from '../xiaomiMappings'
import { homeLocationOptions, matchesDeviceLocation, roomLocationOptions } from '../deviceLocation'

function configuredMappings(provider: Provider): Array<Record<string, unknown>> {
	return Array.isArray(provider.config.devices) ? provider.config.devices.filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object' && !Array.isArray(item))) : []
}

type LocationDraft = { mode: DeviceLocationMode; homeId: string; roomId: string }

function unifiedDeviceID(providerType: string, did: string, type: string, configuredID?: unknown): string {
	if (configuredID) return String(configuredID)
	if (providerType === 'xiaomi-miot-cloud') return stableXiaomiID(did).replace(/^xiaomi-/, 'xiaomi-miot-')
	return type === 'camera' ? stableXiaomiControlID(did) : stableXiaomiID(did)
}

function xiaomiMappingDraft(providerType: string, item: XiaomiHubDevice, type: string, connectionMode: string): Record<string, unknown> {
	const central = providerType === 'xiaomi'
	const id = providerType === 'xiaomi-miot-cloud'
		? stableXiaomiID(item.did).replace(/^xiaomi-/, 'xiaomi-miot-')
		: type === 'camera' ? stableXiaomiControlID(item.did) : stableXiaomiID(item.did)
	const media = central ? undefined : defaultXiaomiMedia(type)
	return {
		did: item.did, id, name: item.name || item.did, type, model: item.model ?? '',
		homeId: item.homeId ?? '', home: item.homeName ?? '', roomId: item.roomId ?? '', room: item.roomName ?? '',
		connectionMode, properties: requiredXiaomiProperties(type), actions: [],
		...(media ? { media } : {}),
	}
}

export function XiaomiDeviceManager({ provider, devices = [], locations = [], onClose, onSave, onMapping, onManageLocations }: {
	provider: Provider
	devices?: Device[]
	locations?: DeviceLocationHome[]
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
	onMapping?: (device: Device) => void
	onManageLocations?: () => void
}) {
	const initialMappings = useMemo(() => configuredMappings(provider), [provider])
	const [mappings, setMappings] = useState<Array<Record<string, unknown>>>(initialMappings)
	const [mappingJSON, setMappingJSON] = useState(JSON.stringify(initialMappings, null, 2))
	const [hubDevices, setHubDevices] = useState<XiaomiHubDevice[]>([])
	const [deviceTypes, setDeviceTypes] = useState<Record<string, string>>(() => Object.fromEntries(initialMappings.map((item) => [String(item.did ?? ''), String(item.type ?? '')]).filter(([did]) => did)))
	const [connectionModes, setConnectionModes] = useState<Record<string, string>>(() => Object.fromEntries(initialMappings.map((item) => [String(item.did ?? ''), String(item.connectionMode ?? 'auto')]).filter(([did]) => did)))
	const [locationDrafts, setLocationDrafts] = useState<Record<string, LocationDraft>>(() => Object.fromEntries(initialMappings.map((item) => {
		const did = String(item.did ?? '')
		const id = unifiedDeviceID(provider.type, did, String(item.type ?? ''), item.id)
		const current = devices.find((device) => device.id === id)
		return [did, current?.locationMode === 'custom'
			? { mode: 'custom', homeId: current.homeId ?? '', roomId: current.roomId ?? '' }
			: { mode: 'source', homeId: '', roomId: '' }]
	}).filter(([did]) => did)))
	const [dirtyLocations, setDirtyLocations] = useState<Set<string>>(() => new Set())
	const [discovering, setDiscovering] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const [deviceHome, setDeviceHome] = useState('')
	const [deviceRoom, setDeviceRoom] = useState('')
	const connected = provider.enabled && provider.status === 'running'
	const central = provider.type === 'xiaomi'
	const mappedDIDs = new Set(mappings.map((item) => String(item.did ?? '')).filter(Boolean))
	const homeOptions = useMemo(() => homeLocationOptions(hubDevices), [hubDevices])
	const roomOptions = useMemo(() => roomLocationOptions(hubDevices, deviceHome), [hubDevices, deviceHome])
	const filteredHubDevices = useMemo(() => hubDevices.filter((item) => matchesDeviceLocation(item, deviceHome, deviceRoom)), [hubDevices, deviceHome, deviceRoom])
	const mappingSubject = (item: Record<string, unknown>): Device => {
		const did = String(item.did ?? '')
		const type = String(item.type || 'switch') as DeviceType
		const fallbackID = unifiedDeviceID(provider.type, did, type)
		return {
			schemaVersion: 1, id: String(item.id || fallbackID), providerId: provider.id,
			name: String(item.name || did || '未命名设备'), type,
			homeId: String(item.homeId ?? ''), homeName: String(item.home ?? ''), roomId: String(item.roomId ?? ''), roomName: String(item.room ?? ''),
			availability: 'unknown', online: false, endpoints: [], lastUpdateAt: new Date(0).toISOString(),
		}
	}

	function replaceMappings(next: Array<Record<string, unknown>>) {
		setMappings(next)
		setMappingJSON(JSON.stringify(next, null, 2))
	}

	function updateLocation(did: string, current: LocationDraft, patch: Partial<LocationDraft>) {
		setLocationDrafts((drafts) => ({ ...drafts, [did]: { ...current, ...patch } }))
		setDirtyLocations((dirty) => new Set(dirty).add(did))
	}

	async function discover() {
		setDiscovering(true); setError(null); setResult(null)
		try {
			const items = await discoverXiaomiDevices(provider.id, provider.type)
			setHubDevices(items)
			setDeviceTypes((current) => ({ ...Object.fromEntries(items.map((item) => {
				const inferred = inferXiaomiDeviceType(item)
				return [item.did, inferred]
			})), ...current }))
			setConnectionModes((current) => ({ ...Object.fromEntries(items.map((item) => [item.did, 'auto'])), ...current }))
			setResult(items.length ? `已通过当前${provider.type === 'xiaomi' ? '中枢 MQTT 与 OAuth 云目录' : '第三方 MIoT 云会话'}读取 ${items.length} 台设备。` : '设备来源返回了空目录。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取小米设备目录') } finally { setDiscovering(false) }
	}

	function addDevice(item: XiaomiHubDevice) {
		if (mappedDIDs.has(item.did)) return
		const inferred = inferXiaomiDeviceType(item)
		const type = deviceTypes[item.did] ?? inferred
		const connectionMode = connectionModes[item.did] ?? 'auto'
		setLocationDrafts((current) => current[item.did] ? current : { ...current, [item.did]: { mode: 'source', homeId: '', roomId: '' } })
		replaceMappings([...mappings, xiaomiMappingDraft(provider.type, item, type, connectionMode)])
	}

	function addUnmappedDrafts() {
		const occupiedIDs = new Set(mappings.map((item) => String(item.id || unifiedDeviceID(provider.type, String(item.did ?? ''), String(item.type ?? 'switch')))))
		const drafts: Array<Record<string, unknown>> = []
		const skipped: string[] = []
		for (const item of hubDevices) {
			// DID is the only identity key used for auto-drafts. Names are never
			// used to merge, replace, or suppress an existing mapping.
			if (mappedDIDs.has(item.did)) continue
			const type = deviceTypes[item.did] ?? inferXiaomiDeviceType(item)
			const draft = xiaomiMappingDraft(provider.type, item, type, connectionModes[item.did] ?? 'auto')
			const id = String(draft.id)
			if (occupiedIDs.has(id)) { skipped.push(item.did); continue }
			occupiedIDs.add(id)
			drafts.push(draft)
		}
		if (drafts.length === 0) {
			setResult(skipped.length ? `未生成草稿：${skipped.length} 台设备的稳定 ID 与现有映射冲突，请手动处理。` : '所有目录设备都已按 DID 映射。')
			return
		}
		setLocationDrafts((current) => ({ ...Object.fromEntries(drafts.map((draft) => [String(draft.did), current[String(draft.did)] ?? { mode: 'source', homeId: '', roomId: '' }])), ...current }))
		replaceMappings([...mappings, ...drafts])
		setResult(`已生成 ${drafts.length} 台未映射设备的草稿；尚未保存。${skipped.length ? `另有 ${skipped.length} 台稳定 ID 冲突，未自动合并。` : ''}`)
	}

	function updateMappedDevice(did: string, field: 'type' | 'connectionMode', value: string) {
			replaceMappings(mappings.map((item) => {
				if (String(item.did ?? '') !== did) return item
				if (field !== 'type') return { ...item, [field]: value }
				const media = central ? undefined : defaultXiaomiMedia(value)
				const oldDefaultID = stableXiaomiID(did)
				const oldControlID = stableXiaomiControlID(did)
				const currentID = String(item.id ?? '')
				const id = central && value === 'camera' && (!currentID || currentID === oldDefaultID)
					? oldControlID
					: central && value !== 'camera' && currentID === oldControlID ? oldDefaultID : currentID
				const next: Record<string, unknown> = { ...item, id, type: value, properties: requiredXiaomiProperties(value) }
				delete next.media
				return { ...next, ...(media ? { media } : {}) }
			}))
		if (field === 'type') setDeviceTypes((current) => ({ ...current, [did]: value }))
		else setConnectionModes((current) => ({ ...current, [did]: value }))
	}

	function removeDevice(did: string) {
		replaceMappings(mappings.filter((item) => String(item.did ?? '') !== did))
	}

	async function save() {
		let parsed: unknown
		try { parsed = JSON.parse(mappingJSON) } catch { setError('设备与 MIoT 映射必须是有效 JSON'); return }
		if (!Array.isArray(parsed)) { setError('设备与 MIoT 映射必须是 JSON 数组'); return }
		const normalized = parsed.map((raw) => {
			if (!central || !raw || typeof raw !== 'object' || Array.isArray(raw)) return raw
			const item = raw as Record<string, unknown>
			const did = String(item.did ?? '')
			if (item.type !== 'camera' || !did || (item.id && item.id !== stableXiaomiID(did))) return item
			return { ...item, id: stableXiaomiControlID(did) }
		})
		for (const [did, location] of Object.entries(locationDrafts)) {
			if (dirtyLocations.has(did) && location.mode === 'custom' && !locations.some((home) => home.id === location.homeId)) {
				setError('HomeLoom 自定义位置必须选择已配置的家庭')
				return
			}
		}
		setSaving(true); setError(null); setResult(null)
		try {
			await onSave({ id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled, config: { ...provider.config, devices: normalized } }, true)
			const next = normalized.filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object' && !Array.isArray(item)))
			for (const item of next) {
				const did = String(item.did ?? '')
				if (!dirtyLocations.has(did)) continue
				const location = locationDrafts[did]
				if (!location) continue
				const id = unifiedDeviceID(provider.type, did, String(item.type ?? ''), item.id)
				await setDeviceLocation(id, location.mode === 'source' ? { mode: 'source' } : { mode: 'custom', homeId: location.homeId, ...(location.roomId ? { roomId: location.roomId } : {}) })
			}
			replaceMappings(next)
			setDirtyLocations(new Set())
			setResult(`已保存 ${next.length} 台子设备映射并实时应用。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '保存子设备映射失败') } finally { setSaving(false) }
	}

	return <section className="xiaomi-device-manager">
		<header><div><p className="eyebrow">XIAOMI · {central ? 'CENTRAL HUB' : 'MIOT CLOUD'}</p><h3>{provider.name} · {central ? '子设备' : '云端设备'}</h3><p>{central ? '目录合并中枢 MQTT 与当前 OAuth 账号云目录；每台设备可选择本地优先、仅本地或仅云端。' : '设备目录通过独立的第三方兼容 MIoT 云会话读取。'}映射保存后进入统一设备模型。</p></div><button onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? (central ? 'MQTT 已连接' : '云会话可用') : (central ? 'MQTT 尚未连接' : '云会话尚未连接')}</strong><small>{central ? `${provider.id} · ${String(provider.config.host || '未配置中枢')}:${Number(provider.config.port || 8883)}` : `${provider.id} · ${String(provider.config.region || 'cn').toUpperCase()} · 第三方兼容接口`}</small></div><button disabled={!connected || discovering} onClick={() => void discover()}>{discovering ? '正在读取…' : hubDevices.length ? '刷新设备目录' : central ? '从中枢读取子设备' : '从 MIoT 云读取设备'}</button></div>
		{!connected && <p className="inline-error" role="alert">请先完成{central ? ' OAuth、证书和 MQTT' : '小米账号或会话凭据'}配置并启用 Provider；状态变为 running 后才能读取设备目录。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success">{result}</p>}
		<div className="xiaomi-device-binding">
			<div className="xiaomi-device-binding__heading"><div><strong>{central ? '中枢与官方云设备目录' : 'MIoT 云设备目录'}</strong><small>选择统一模型和设备级连接策略；已映射设备也可以直接调整。</small></div><span>{filteredHubDevices.length} / {hubDevices.length} 台</span>{hubDevices.length > 0 && <button type="button" disabled={saving} onClick={addUnmappedDrafts}>为未映射设备生成草稿</button>}</div>
			{hubDevices.length > 0 && <div className="device-picker-filters" aria-label="小米设备位置筛选"><label>家庭<select aria-label="小米设备家庭" value={deviceHome} onChange={(event) => { setDeviceHome(event.target.value); setDeviceRoom('') }}><option value="">全部家庭</option>{homeOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><label>房间<select aria-label="小米设备房间" value={deviceRoom} onChange={(event) => setDeviceRoom(event.target.value)}><option value="">全部房间</option>{roomOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label></div>}
			{hubDevices.length === 0 ? <p className="xiaomi-device-binding__empty">连接就绪后读取设备目录，再选择需要作为来源发布的设备。</p> : filteredHubDevices.length === 0 ? <p className="xiaomi-device-binding__empty">当前家庭和房间下没有可选设备。</p> : <div className="xiaomi-hub-device-list">{filteredHubDevices.map((item) => {
				const mapped = mappedDIDs.has(item.did)
				const location = `${item.homeName || '未知家庭'} / ${item.roomName || '未分配房间'}`
				const localAvailable = central ? item.localControlAvailable !== false && item.gatewayAvailable !== false : Boolean(item.localAvailable)
				const cloudAvailable = central ? item.cloudAvailable !== false : true
				const inferred = inferXiaomiDeviceType(item)
				const type = deviceTypes[item.did] ?? inferred
				const connectionMode = connectionModes[item.did] ?? 'auto'
				const locationDraft = locationDrafts[item.did] ?? { mode: 'source', homeId: '', roomId: '' }
				const selectedHome = locations.find((home) => home.id === locationDraft.homeId)
				return <article key={item.did}><div><strong>{item.name || item.did}</strong><small>{location} · {item.model || item.specType || '型号未知'}</small>{central ? <span className="xiaomi-route-capabilities"><i className={localAvailable ? 'is-ready' : ''}>{localAvailable ? '中枢本地可控' : '中枢仅发现'}</i><i className={cloudAvailable ? 'is-ready' : ''}>{cloudAvailable ? 'OAuth 官方云可用' : '云目录未发现'}</i><i className={item.pushAvailable ? 'is-ready' : ''}>{item.pushAvailable ? '中枢实时' : '本地需要校准'}</i>{cloudAvailable && <i className={provider.metrics?.cloudMqttConnected ? 'is-ready' : ''}>{provider.metrics?.cloudMqttConnected ? '官方云实时' : '官方云 HTTP 补偿'}</i>}</span> : <small>{item.localAvailable ? `局域网 MIoT 可用 · ${item.localIp}` : '未取得局域网 IP/Token，将使用云端'}</small>}<code>{item.did}</code>{central && type === 'camera' && <small>此映射只提供中枢/云端控制能力；视频仍由独立 Camera Provider 获取。</small>}</div><label>统一模型（deviceType）<select aria-label={`${item.name || item.did} 统一模型`} value={type} onChange={(event) => mapped ? updateMappedDevice(item.did, 'type', event.target.value) : setDeviceTypes((current) => ({ ...current, [item.did]: event.target.value }))}>{xiaomiDeviceTypes.map(([value, label]) => <option value={value} key={value}>{label}（{value}）</option>)}</select></label><label>连接策略（connectionMode）<select aria-label={`${item.name || item.did} 连接策略`} value={connectionMode} onChange={(event) => mapped ? updateMappedDevice(item.did, 'connectionMode', event.target.value) : setConnectionModes((current) => ({ ...current, [item.did]: event.target.value }))}><option value="auto">自动：本地优先，云端回退（auto）</option><option value="local" disabled={!localAvailable}>仅局域网/中枢（local）</option><option value="cloud" disabled={!cloudAvailable}>仅云端（cloud）</option></select></label><div className="provider-device-location"><label>统一设备位置<select aria-label={`${item.name || item.did} 位置策略`} value={locationDraft.mode} onChange={(event) => { const mode = event.target.value as DeviceLocationMode; updateLocation(item.did, locationDraft, { mode, ...(mode === 'custom' ? { homeId: locationDraft.homeId || locations[0]?.id || '', roomId: '' } : {}) }) }}><option value="source">继承来源：{location}</option><option value="custom">选择 HomeLoom 位置</option></select></label>{locationDraft.mode === 'custom' && <><label>家庭<select aria-label={`${item.name || item.did} HomeLoom 家庭`} value={locationDraft.homeId} onChange={(event) => updateLocation(item.did, locationDraft, { homeId: event.target.value, roomId: '' })}><option value="">请选择家庭</option>{locations.map((home) => <option value={home.id} key={home.id}>{home.name}</option>)}</select></label><label>房间<select aria-label={`${item.name || item.did} HomeLoom 房间`} value={locationDraft.roomId} disabled={!selectedHome} onChange={(event) => updateLocation(item.did, locationDraft, { roomId: event.target.value })}><option value="">不指定房间</option>{selectedHome?.rooms.map((room) => <option value={room.id} key={room.id}>{room.name}</option>)}</select></label>{onManageLocations && <button type="button" onClick={onManageLocations}>管理位置</button>}</>}</div>{mapped ? <button type="button" className="is-danger" onClick={() => removeDevice(item.did)}>移除映射</button> : <button type="button" onClick={() => addDevice(item)}>加入映射</button>}</article>
			})}</div>}
			<div className="xiaomi-mapped-summary"><strong>已映射 {mappings.length} 台设备</strong><small>“生成草稿”只按 DID 去重，不会按名称合并；草稿必须点击保存才会写入 Provider 配置。自动模板只覆盖统一模型必需参数，仍需按具体型号核对 SIID、PIID 和 AIID。即使设备因映射错误未进入设备中心，也可以从这里修正或删除属性路由。</small>{onMapping && mappings.length > 0 && <div className="xiaomi-mapped-summary__devices">{mappings.map((item) => { const subject = mappingSubject(item); return <button type="button" key={subject.id} aria-label={`配置 ${subject.name} 属性映射`} onClick={() => onMapping(subject)}><span>{subject.name}</span><code>{subject.id}</code><small>{subject.type}</small></button> })}</div>}</div>
		</div>
		<details><summary>设备与 MIoT 映射（高级 JSON）</summary><label className="config-editor"><textarea aria-label="小米设备映射" rows={16} value={mappingJSON} onChange={(event) => { setMappingJSON(event.target.value); try { const parsed = JSON.parse(event.target.value) as unknown; if (Array.isArray(parsed)) setMappings(parsed.filter((item): item is Record<string, unknown> => Boolean(item && typeof item === 'object' && !Array.isArray(item)))) } catch { /* save reports invalid JSON */ } }} spellCheck={false} /><small>属性使用 siid/piid，Action 使用 siid/aiid。连接与账号凭据不在本页面编辑。</small></label></details>
		<div className="form-actions"><button onClick={onClose}>取消</button><button className="primary" disabled={saving || !connected} onClick={() => void save()}>{saving ? '正在应用…' : '保存子设备映射'}</button></div>
	</section>
}
