import { useState } from 'react'
import type { Device } from '../types/device'
import type { Target, TargetInput, TargetType } from '../types/target'
import { ApiError } from '../api/client'

interface Props {
	target: Target | null
	devices: Device[]
	onCancel: () => void
	onSave: (input: TargetInput, editing: boolean) => Promise<void>
}

export function TargetForm({ target, devices, onCancel, onSave }: Props) {
	const editing = target !== null
	const [form, setForm] = useState<TargetInput>({
		id: target?.id || '', type: target?.type || 'apple-hap', name: target?.name || '',
		enabled: target?.enabled ?? true, address: target?.address || '',
		pin: target?.pairingCode?.replaceAll('-', '') || '', setupId: target?.setupId || '',
		deviceIds: target?.deviceIds || [],
	})
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
	const update = <K extends keyof TargetInput>(key: K, value: TargetInput[K]) => setForm((current) => ({ ...current, [key]: value }))
	const toggleDevice = (id: string) => update('deviceIds', form.deviceIds.includes(id) ? form.deviceIds.filter((item) => item !== id) : [...form.deviceIds, id])

	return <div className="modal-backdrop" role="presentation">
	  <form className="target-form" role="dialog" aria-modal="true" aria-labelledby="target-form-title" onSubmit={(event) => {
		event.preventDefault(); setSaving(true); setError(null); setFieldErrors({})
		void onSave(form, editing).catch((cause) => { setError(cause instanceof Error ? cause.message : '保存失败'); if (cause instanceof ApiError) setFieldErrors(cause.fields) }).finally(() => setSaving(false))
	  }}>
		<div className="form-heading"><div><p className="eyebrow">TARGET CONFIG</p><h2 id="target-form-title">{editing ? '编辑桥' : '新建桥'}</h2></div><button type="button" onClick={onCancel}>关闭</button></div>
		<div className="form-grid">
		  <label>桥 ID<input aria-invalid={Boolean(fieldErrors.id)} value={form.id} disabled={editing} placeholder="留空自动生成" onChange={(e) => update('id', e.target.value)} />{fieldErrors.id && <small className="field-error">{fieldErrors.id}</small>}</label>
		  <label>类型<select aria-invalid={Boolean(fieldErrors.type)} value={form.type} onChange={(e) => update('type', e.target.value as TargetType)}><option value="apple-hap">Apple HAP</option><option value="matter">Matter（预留）</option></select>{fieldErrors.type && <small className="field-error">{fieldErrors.type}</small>}</label>
		  <label className="wide">名称<input aria-invalid={Boolean(fieldErrors.name)} value={form.name} placeholder="留空使用 HomeLoom Bridge" onChange={(e) => update('name', e.target.value)} />{fieldErrors.name && <small className="field-error">{fieldErrors.name}</small>}</label>
		  <label>监听地址<input aria-invalid={Boolean(fieldErrors.address)} value={form.address} placeholder="留空自动分配端口" onChange={(e) => update('address', e.target.value)} />{fieldErrors.address && <small className="field-error">{fieldErrors.address}</small>}</label>
		  <label>Setup ID<input aria-invalid={Boolean(fieldErrors.setupId)} value={form.setupId} maxLength={4} placeholder="留空自动生成" onChange={(e) => update('setupId', e.target.value.toUpperCase())} />{fieldErrors.setupId && <small className="field-error">{fieldErrors.setupId}</small>}</label>
		  <label>8 位 PIN<input aria-invalid={Boolean(fieldErrors.pin)} value={form.pin} maxLength={8} placeholder="留空自动生成" onChange={(e) => update('pin', e.target.value)} />{fieldErrors.pin && <small className="field-error">{fieldErrors.pin}</small>}</label>
		</div>
		<label className="enable-row"><input type="checkbox" checked={form.enabled} onChange={(e) => update('enabled', e.target.checked)} />启用此桥</label>
		{error && <p className="inline-error">{error}</p>}
		<fieldset><legend>绑定设备</legend><p>不选择表示发布全部设备；选择后只发布勾选设备。</p>{devices.map((device) => <label key={device.id}><input type="checkbox" checked={form.deviceIds.includes(device.id)} onChange={() => toggleDevice(device.id)} /><span>{device.name}</span><small>{device.id}</small></label>)}</fieldset>
		<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '保存中…' : '保存到数据库'}</button></div>
	  </form>
	</div>
}
