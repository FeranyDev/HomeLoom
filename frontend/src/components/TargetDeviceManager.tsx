import { useMemo, useState } from 'react'
import type { Device, DeviceType } from '../types/device'
import type { Target, TargetInput, TargetVirtualDevice } from '../types/target'
import { BindingManager } from './BindingManager'
import { deviceTypeLabel } from '../presentationLabels'

function inputFromTarget(target: Target, devices: TargetVirtualDevice[]): TargetInput {
	return { id: target.id, type: target.type, name: target.name, enabled: target.enabled, address: target.address ?? '', pin: target.pairingCode?.replaceAll('-', '') ?? '', setupId: target.setupId ?? '', deviceIds: [], devices }
}

function stableID(value: string): string { return value.toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64) }

export function TargetDeviceManager({ target, devices, onClose, onSave }: { target: Target; devices: Device[]; onClose: () => void; onSave: (input: TargetInput) => Promise<void> }) {
	const [items, setItems] = useState<TargetVirtualDevice[]>(target.devices ?? [])
	const [sourceDeviceId, setSourceDeviceId] = useState(devices[0]?.id ?? '')
	const [id, setID] = useState('')
	const [name, setName] = useState('')
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [mappingID, setMappingID] = useState<string | null>(null)
	const source = devices.find((item) => item.id === sourceDeviceId)
	const mappingItem = items.find((item) => item.id === mappingID)
	const mappingSource = mappingItem ? devices.find((item) => item.id === mappingItem.sourceDeviceId) : undefined
	const persistedIDs = useMemo(() => new Set((target.devices ?? []).map((item) => item.id)), [target.devices])
	const availableSources = useMemo(() => devices.filter((item) => !item.removed), [devices])

	function add() {
		if (!source) { setError('请选择统一模型来源设备'); return }
		const requestedID = stableID(id)
		const baseID = requestedID || stableID(`${target.id}-${source.id}`)
		let nextID = baseID
		for (let suffix = 2; !requestedID && items.some((item) => item.id === nextID); suffix += 1) {
			const suffixText = `-${suffix}`
			nextID = `${baseID.slice(0, 64 - suffixText.length)}${suffixText}`
		}
		if (!nextID || items.some((item) => item.id === nextID)) { setError('虚拟设备 ID 无效或重复'); return }
		setItems((current) => [...current, { id: nextID, name: name.trim() || source.name, type: source.type as DeviceType, sourceDeviceId: source.id, enabled: true }])
		setID(''); setName(''); setError(null)
		setSourceDeviceId(source.id)
	}

	async function save() {
		setSaving(true); setError(null)
		try { await onSave(inputFromTarget(target, items)) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存虚拟设备失败') } finally { setSaving(false) }
	}

	return <section className="target-device-manager">
		<header><div><p className="eyebrow">目标桥 · 虚拟设备（TARGET · VIRTUAL DEVICES）</p><h3>{target.name} · 虚拟设备</h3><p>先在桥内创建设备身份，再进入每台虚拟设备配置“统一模型 → 消费端（Consumer）”属性映射。</p></div><button onClick={onClose}>返回桥接中心</button></header>
		{error && <p className="inline-error" role="alert">{error}</p>}
		<div className="target-device-create"><label>来源统一设备（sourceDeviceId）<select aria-label="来源统一设备" value={sourceDeviceId} onChange={(event) => setSourceDeviceId(event.target.value)}><option value="">请选择</option>{availableSources.map((item) => <option key={item.id} value={item.id}>{item.name} · {deviceTypeLabel(item.type)} · {item.id}</option>)}</select></label><label>虚拟设备标识（ID）<input value={id} placeholder="留空自动生成" onChange={(event) => setID(event.target.value)} /></label><label>显示名称（name）<input value={name} placeholder={source?.name ?? '虚拟设备名称'} onChange={(event) => setName(event.target.value)} /></label><button disabled={!source} onClick={add}>＋ 添加虚拟设备</button></div>
		<div className="target-virtual-device-list"><div className="command-heading"><h3>桥内设备</h3><span>{items.length} 台</span></div>{items.length === 0 ? <p>当前桥还没有虚拟设备。</p> : items.map((item) => <article key={item.id}><div><span className={`status-dot ${item.enabled ? 'is-online' : ''}`} /><input aria-label={`${item.id} 显示名称`} value={item.name} onChange={(event) => setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, name: event.target.value } : candidate))} /><code>{item.id}</code><small>{deviceTypeLabel(item.type)} · 来源设备（source）{item.sourceDeviceId}{persistedIDs.has(item.id) ? '' : ' · 待保存'}</small></div><div><button onClick={() => setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, enabled: !candidate.enabled } : candidate))}>{item.enabled ? '停用' : '启用'}</button><button disabled={!persistedIDs.has(item.id)} title={persistedIDs.has(item.id) ? undefined : '请先保存虚拟设备'} onClick={() => setMappingID(mappingID === item.id ? null : item.id)}>{mappingID === item.id ? '收起映射' : persistedIDs.has(item.id) ? '配置属性映射' : '先保存再映射'}</button><button className="danger-link" onClick={() => { setItems((current) => current.filter((candidate) => candidate.id !== item.id)); if (mappingID === item.id) setMappingID(null) }}>删除</button></div></article>)}</div>
		{mappingItem && mappingSource && <BindingManager device={mappingSource} initialStage="consumer" consumerOnly consumerLabel={`${mappingItem.name} · 属性映射`} targetId={target.id} consumerDeviceId={mappingItem.id} />}
		<div className="form-actions"><button onClick={onClose}>取消</button><button className="primary" disabled={saving} onClick={() => void save()}>{saving ? '正在应用…' : '保存虚拟设备并重建桥'}</button></div>
	</section>
}
