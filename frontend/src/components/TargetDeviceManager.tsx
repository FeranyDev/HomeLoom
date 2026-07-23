import { useEffect, useMemo, useState } from 'react'
import type { Device, DeviceType } from '../types/device'
import type { Target, TargetInput, TargetVirtualDevice } from '../types/target'
import { ApiError } from '../api/client'
import { listConsumerCatalogs } from '../api/mapping'
import { BindingManager } from './BindingManager'
import { deviceTypeLabel } from '../presentationLabels'
import { targetDescriptor } from '../targetDescriptors'
import { deviceLocationLabel, homeLocationOptions, matchesDeviceLocation, roomLocationOptions } from '../deviceLocation'
import { confirmExactPhrase } from '../confirmations'

function inputFromTarget(target: Target, devices: TargetVirtualDevice[]): TargetInput {
	if (target.type === 'matter') {
		return {
			id: target.id, type: 'matter', name: target.name, enabled: target.enabled, deviceIds: [], devices,
			config: {
				networkInterface: target.config.networkInterface ?? '', udpPort: target.config.udpPort ?? null, discriminator: target.config.discriminator ?? null,
				passcode: null, vendorId: target.config.vendorId ?? null, productId: target.config.productId ?? null,
				productName: target.config.productName ?? '', serialNumber: target.config.serialNumber ?? '', commissioningWindowSeconds: target.config.commissioningWindowSeconds ?? null,
			},
		}
	}
	return { id: target.id, type: 'apple-hap', name: target.name, enabled: target.enabled, config: { address: target.config.address ?? '', pin: '', setupId: target.config.setupId ?? '' }, deviceIds: [], devices }
}

function stableID(value: string): string { return value.toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64) }

function saveErrorMessage(cause: unknown): string {
	if (cause instanceof ApiError && Object.keys(cause.fields).length > 0) {
		const details = Object.entries(cause.fields).map(([field, message]) => `${field}: ${message}`).join('；')
		return `${cause.message}：${details}`
	}
	return cause instanceof Error ? cause.message : '保存消费端设备失败'
}

export function TargetDeviceManager({ target, devices, onClose, onSave, onConfirmMatterEndpointType }: { target: Target; devices: Device[]; onClose: () => void; onSave: (input: TargetInput) => Promise<void>; onConfirmMatterEndpointType?: (consumerDeviceID: string, deviceType: DeviceType, confirmation: string) => Promise<void> }) {
	const descriptor = targetDescriptor(target.type)
	const consumerId = target.consumerId ?? descriptor.consumerId
	const [items, setItems] = useState<TargetVirtualDevice[]>(target.devices ?? [])
	const [sourceDeviceId, setSourceDeviceId] = useState(devices[0]?.id ?? '')
	const [auxiliarySourceDeviceIds, setAuxiliarySourceDeviceIds] = useState<string[]>([])
	const [deviceType, setDeviceType] = useState<DeviceType | ''>(devices[0]?.type ?? '')
	const [id, setID] = useState('')
	const [name, setName] = useState('')
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [mappingID, setMappingID] = useState<string | null>(null)
	const [supportedTypes, setSupportedTypes] = useState<Set<DeviceType> | null>(null)
	const [sourceHome, setSourceHome] = useState('')
	const [sourceRoom, setSourceRoom] = useState('')
	const source = devices.find((item) => item.id === sourceDeviceId)
	const sourceType = source?.type
	const mappingItem = items.find((item) => item.id === mappingID)
	const mappingSources = mappingItem ? [mappingItem.sourceDeviceId, ...(mappingItem.auxiliarySourceDeviceIds ?? [])].map((sourceID) => devices.find((item) => item.id === sourceID)).filter((item): item is Device => Boolean(item)) : []
	const persistedIDs = useMemo(() => new Set((target.devices ?? []).map((item) => item.id)), [target.devices])
	const availableSources = useMemo(() => devices.filter((item) => !item.removed), [devices])
	const sourceHomeOptions = useMemo(() => homeLocationOptions(availableSources), [availableSources])
	const sourceRoomOptions = useMemo(() => roomLocationOptions(availableSources, sourceHome), [availableSources, sourceHome])
	const filteredSources = useMemo(() => availableSources.filter((item) => matchesDeviceLocation(item, sourceHome, sourceRoom)), [availableSources, sourceHome, sourceRoom])
	const supportedTypeList = useMemo(() => supportedTypes ? Array.from(supportedTypes) : [], [supportedTypes])
	const sourcesWithSelections = (selectedIDs: string[]) => availableSources.filter((item) => filteredSources.some((candidate) => candidate.id === item.id) || selectedIDs.includes(item.id))

	useEffect(() => {
		let active = true
		void listConsumerCatalogs().then((catalogs) => {
			if (!active) return
			const consumer = catalogs.find((item) => item.id === consumerId)
			const nextTypes = new Set((consumer?.properties ?? []).map((item) => item.deviceType))
			setSupportedTypes(nextTypes)
			setDeviceType((current) => current && nextTypes.has(current) ? current : sourceType && nextTypes.has(sourceType) ? sourceType : Array.from(nextTypes)[0] ?? '')
		}).catch(() => { /* saving still uses server-side validation when the catalog is temporarily unavailable */ })
		return () => { active = false }
	}, [consumerId, sourceType])

	useEffect(() => {
		setAuxiliarySourceDeviceIds((current) => current.filter((sourceID) => sourceID !== sourceDeviceId && availableSources.some((item) => item.id === sourceID)))
	}, [availableSources, sourceDeviceId])

	useEffect(() => {
		if (!filteredSources.some((item) => item.id === sourceDeviceId)) setSourceDeviceId(filteredSources[0]?.id ?? '')
	}, [filteredSources, sourceDeviceId])

	function add() {
		if (!source) { setError('请选择统一模型来源设备'); return }
		const requestedID = stableID(id)
		const baseID = requestedID || stableID(`${target.id}-${source.id}`)
		let nextID = baseID
		for (let suffix = 2; !requestedID && items.some((item) => item.id === nextID); suffix += 1) {
			const suffixText = `-${suffix}`
			nextID = `${baseID.slice(0, 64 - suffixText.length)}${suffixText}`
		}
		if (!nextID || items.some((item) => item.id === nextID)) { setError('消费端设备 ID 无效或重复'); return }
		if (!deviceType || (supportedTypes && !supportedTypes.has(deviceType))) { setError('请选择当前目标适配器支持的设备类型'); return }
		setItems((current) => [...current, { id: nextID, name: name.trim() || source.name, type: deviceType, sourceDeviceId: source.id, auxiliarySourceDeviceIds: auxiliarySourceDeviceIds, enabled: true }])
		setID(''); setName(''); setError(null)
		setAuxiliarySourceDeviceIds([])
		setSourceDeviceId(source.id)
	}

	async function save() {
		setSaving(true); setError(null)
		try {
			const changes = target.type === 'matter' ? items.flatMap((item) => {
				const previous = target.devices.find((candidate) => candidate.id === item.id)
				return previous && previous.type !== item.type ? [{ item, previousType: previous.type }] : []
			}) : []
			if (changes.length > 0 && !onConfirmMatterEndpointType) throw new Error('Matter Endpoint Device Type 变更确认服务不可用；请保留原类型，或删除后新建消费端设备。')
			const confirmations: string[] = []
			for (const { item } of changes) {
				const phrase = `CHANGE ENDPOINT TYPE ${target.id} ${item.id} ${item.type}`
				const confirmation = confirmExactPhrase('变更 Matter Endpoint 的 Device Type 会重建该 Endpoint 的协议结构；控制器可能需要重新发现设备。', phrase)
				if (!confirmation) throw new Error(`未确认 ${item.id} 的 Device Type 变更；已取消保存。`)
				confirmations.push(confirmation)
			}
			const baseItems = items.map((item) => {
				const change = changes.find((candidate) => candidate.item.id === item.id)
				return change ? { ...item, type: change.previousType } : item
			})
			await onSave(inputFromTarget(target, baseItems))
			for (let index = 0; index < changes.length; index += 1) {
				const item = changes[index].item
				await onConfirmMatterEndpointType!(item.id, item.type, confirmations[index])
			}
		} catch (cause) { setError(saveErrorMessage(cause)) } finally { setSaving(false) }
	}

	return <section className="target-device-manager">
		<header><div><p className="eyebrow">目标实例 · 消费端设备（TARGET · CONSUMER DEVICES）</p><h3>{target.name} · 消费端设备</h3><p>目标适配器：{descriptor.consumerName}（{consumerId}）。先创建设备身份，再为每台设备配置“统一模型 → 消费端”属性映射。</p></div><button onClick={onClose}>返回桥接中心</button></header>
		{error && <p className="inline-error" role="alert">{error}</p>}
		<div className="device-picker-filters" aria-label="来源设备位置筛选"><label>家庭<select aria-label="来源设备家庭" value={sourceHome} onChange={(event) => { setSourceHome(event.target.value); setSourceRoom('') }}><option value="">全部家庭</option>{sourceHomeOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><label>房间<select aria-label="来源设备房间" value={sourceRoom} onChange={(event) => setSourceRoom(event.target.value)}><option value="">全部房间</option>{sourceRoomOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label><span>{filteredSources.length} / {availableSources.length} 台可选</span></div>
		<div className="target-device-create"><label>主来源统一设备（sourceDeviceId）<select aria-label="来源统一设备" value={sourceDeviceId} onChange={(event) => setSourceDeviceId(event.target.value)}><option value="">请选择</option>{filteredSources.map((item) => <option key={item.id} value={item.id}>{item.name} · {deviceTypeLabel(item.type)} · {deviceLocationLabel(item)} · {item.id}</option>)}</select></label><label>消费端设备类型（type）<select aria-label="消费端设备类型" value={deviceType} onChange={(event) => setDeviceType(event.target.value as DeviceType)}><option value="">请选择</option>{supportedTypeList.map((type) => <option key={type} value={type}>{deviceTypeLabel(type)} · {type}</option>)}</select></label><label>消费端设备标识（ID）<input value={id} placeholder="留空自动生成" onChange={(event) => setID(event.target.value)} /></label><label>显示名称（name）<input value={name} placeholder={source?.name ?? '消费端设备名称'} onChange={(event) => setName(event.target.value)} /></label><fieldset className="target-auxiliary-sources"><legend>辅助来源设备（可多选）</legend>{sourcesWithSelections(auxiliarySourceDeviceIds).filter((item) => item.id !== sourceDeviceId).map((item) => <label key={item.id}><input type="checkbox" checked={auxiliarySourceDeviceIds.includes(item.id)} onChange={(event) => setAuxiliarySourceDeviceIds((current) => event.target.checked ? [...current, item.id] : current.filter((id) => id !== item.id))} />{item.name} · {deviceTypeLabel(item.type)} · {deviceLocationLabel(item)}</label>)}</fieldset><button disabled={!source || !deviceType || Boolean(supportedTypes && !supportedTypes.has(deviceType))} onClick={add}>＋ 添加消费端设备</button><small className="wide consumer-support-note">设备类型属于 {descriptor.consumerName} 虚拟设备，可与来源统一模型类型不同；转换和属性选择在保存后的映射页配置。</small></div>
		<div className="target-virtual-device-list"><div className="command-heading"><h3>目标实例内设备</h3><span>{items.length} 台</span></div>{items.length === 0 ? <p>当前目标实例还没有消费端设备。</p> : items.map((item) => <article key={item.id}><div><span className={`status-dot ${item.enabled ? 'is-online' : ''}`} /><input aria-label={`${item.id} 显示名称`} value={item.name} onChange={(event) => setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, name: event.target.value } : candidate))} /><code>{item.id}</code><label className="target-device-type-edit">设备类型<select aria-label={`${item.id} 设备类型`} value={item.type} onChange={(event) => setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, type: event.target.value as DeviceType } : candidate))}>{supportedTypeList.map((type) => <option key={type} value={type}>{deviceTypeLabel(type)} · {type}</option>)}</select></label><small>主来源 {item.sourceDeviceId} · 辅助来源 {(item.auxiliarySourceDeviceIds ?? []).join('、') || '无'}{persistedIDs.has(item.id) ? '' : ' · 待保存'}</small><details className="target-device-sources-edit"><summary>编辑主来源和辅助来源</summary><label>主来源<select aria-label={`${item.id} 主来源`} value={item.sourceDeviceId} onChange={(event) => setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, sourceDeviceId: event.target.value, auxiliarySourceDeviceIds: (candidate.auxiliarySourceDeviceIds ?? []).filter((sourceID) => sourceID !== event.target.value) } : candidate))}>{sourcesWithSelections([item.sourceDeviceId]).map((sourceItem) => <option key={sourceItem.id} value={sourceItem.id}>{sourceItem.name} · {deviceLocationLabel(sourceItem)} · {sourceItem.id}</option>)}</select></label><fieldset><legend>辅助来源</legend>{sourcesWithSelections(item.auxiliarySourceDeviceIds ?? []).filter((sourceItem) => sourceItem.id !== item.sourceDeviceId).map((sourceItem) => <label key={sourceItem.id}><input type="checkbox" checked={(item.auxiliarySourceDeviceIds ?? []).includes(sourceItem.id)} onChange={(event) => setItems((current) => current.map((candidate) => candidate.id !== item.id ? candidate : { ...candidate, auxiliarySourceDeviceIds: event.target.checked ? [...(candidate.auxiliarySourceDeviceIds ?? []), sourceItem.id] : (candidate.auxiliarySourceDeviceIds ?? []).filter((sourceID) => sourceID !== sourceItem.id) }))} />{sourceItem.name} · {deviceTypeLabel(sourceItem.type)} · {deviceLocationLabel(sourceItem)}</label>)}</fieldset></details></div><div><button onClick={() => setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, enabled: !candidate.enabled } : candidate))}>{item.enabled ? '停用' : '启用'}</button><button disabled={!persistedIDs.has(item.id)} title={persistedIDs.has(item.id) ? undefined : '请先保存消费端设备'} onClick={() => setMappingID(mappingID === item.id ? null : item.id)}>{mappingID === item.id ? '收起映射' : persistedIDs.has(item.id) ? '配置属性映射' : '先保存再映射'}</button><button className="danger-link" onClick={() => { setItems((current) => current.filter((candidate) => candidate.id !== item.id)); if (mappingID === item.id) setMappingID(null) }}>删除</button></div></article>)}</div>
		{mappingItem && mappingSources.length > 0 && <section className="target-source-mappings"><header><h3>{mappingItem.name} · 多来源属性映射</h3><p>按“主来源 → 辅助来源”确定优先级；相同消费端属性存在多条手动映射时，优先使用排在前面的来源。手动映射始终覆盖主来源的同路径默认映射。</p></header>{mappingSources.map((mappingSource, index) => <details key={mappingSource.id} open={index === 0}><summary>{index === 0 ? '主来源' : `辅助来源 ${index}`} · {mappingSource.name} · {deviceTypeLabel(mappingSource.type)}</summary><BindingManager device={mappingSource} initialStage="consumer" consumerOnly consumerId={consumerId} consumerDeviceType={mappingItem.type} consumerLabel={`${mappingItem.name} · ${mappingSource.name} → ${descriptor.consumerName}`} targetId={target.id} consumerDeviceId={mappingItem.id} /></details>)}</section>}
		<div className="form-actions"><button onClick={onClose}>取消</button><button className="primary" disabled={saving} onClick={() => void save()}>{saving ? '正在应用…' : '保存消费端设备并应用目标'}</button></div>
	</section>
}
