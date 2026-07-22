import { useState } from 'react'
import type { Target, TargetInput, TargetType } from '../types/target'
import { ApiError } from '../api/client'
import { targetTypeLabel } from '../presentationLabels'
import { targetDescriptor } from '../targetDescriptors'

interface Props {
	target: Target | null
	onCancel: () => void
	onSave: (input: TargetInput, editing: boolean) => Promise<void>
}

export function TargetForm({ target, onCancel, onSave }: Props) {
	const editing = target !== null
	const [form, setForm] = useState<TargetInput>({
		id: target?.id || '', type: target?.type || 'apple-hap', name: target?.name || '',
		enabled: target?.enabled ?? true, address: target?.address || '',
		pin: target?.pairingCode?.replaceAll('-', '') || '', setupId: target?.setupId || '',
		deviceIds: target?.deviceIds || [],
		devices: target?.devices || [],
	})
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
	const update = <K extends keyof TargetInput>(key: K, value: TargetInput[K]) => setForm((current) => ({ ...current, [key]: value }))
	const descriptor = targetDescriptor(form.type)
	const pairingLocked = editing && descriptor.supportsHomeKitPairing && Boolean(target?.paired)

	return <div className="modal-backdrop" role="presentation">
	  <form className="target-form" role="dialog" aria-modal="true" aria-labelledby="target-form-title" onSubmit={(event) => {
		event.preventDefault(); setSaving(true); setError(null); setFieldErrors({})
		void onSave(form, editing).catch((cause) => { setError(cause instanceof Error ? cause.message : '保存失败'); if (cause instanceof ApiError) setFieldErrors(cause.fields) }).finally(() => setSaving(false))
	  }}>
		<div className="form-heading"><div><p className="eyebrow">目标实例配置（TARGET CONFIG）</p><h2 id="target-form-title">{editing ? '编辑目标实例' : '新建目标实例'}</h2></div><button type="button" onClick={onCancel}>关闭</button></div>
		<div className="form-grid">
		  <label>目标实例标识（ID）<input aria-invalid={Boolean(fieldErrors.id)} value={form.id} disabled={editing} placeholder="留空自动生成" onChange={(e) => update('id', e.target.value)} />{fieldErrors.id && <small className="field-error">{fieldErrors.id}</small>}</label>
		  <label>目标类型（type）<select aria-invalid={Boolean(fieldErrors.type)} value={form.type} disabled={editing} onChange={(e) => { const type = e.target.value as TargetType; setForm((current) => ({ ...current, type, ...(type === 'apple-hap' ? {} : { address: '', pin: '', setupId: '' }) })) }}><option value="apple-hap">{targetTypeLabel('apple-hap')}</option><option value="matter">{targetTypeLabel('matter')} · 预留</option></select>{fieldErrors.type && <small className="field-error">{fieldErrors.type}</small>}</label>
		  <label className="wide">名称<input aria-invalid={Boolean(fieldErrors.name)} value={form.name} placeholder="留空自动生成目标名称" onChange={(e) => update('name', e.target.value)} />{fieldErrors.name && <small className="field-error">{fieldErrors.name}</small>}</label>
		  {descriptor.supportsHomeKitPairing && <><label>HAP 监听地址（address）<input aria-invalid={Boolean(fieldErrors.address)} value={form.address} placeholder="留空自动分配端口" onChange={(e) => update('address', e.target.value)} />{fieldErrors.address && <small className="field-error">{fieldErrors.address}</small>}</label>{!pairingLocked && <><label>HomeKit 设置标识（Setup ID）<input aria-invalid={Boolean(fieldErrors.setupId)} value={form.setupId} maxLength={4} placeholder="留空自动生成" onChange={(e) => update('setupId', e.target.value.toUpperCase())} />{fieldErrors.setupId && <small className="field-error">{fieldErrors.setupId}</small>}</label><label>HomeKit 8 位配对码（PIN）<input aria-invalid={Boolean(fieldErrors.pin)} value={form.pin} maxLength={8} placeholder="留空自动生成" onChange={(e) => update('pin', e.target.value)} />{fieldErrors.pin && <small className="field-error">{fieldErrors.pin}</small>}</label></>}</>}
		</div>
		{pairingLocked && <div className="config-note pairing-locked-note"><span>HomeKit 已配对</span><strong>一次性配对参数已隐藏并锁定</strong><p>PIN 和 Setup ID 在已加入 Apple Home 后不再用于日常运行。如需重新配对，请先在桥卡片上清除配对身份。</p></div>}
		{!descriptor.implemented && <div className="config-note"><span>目标适配器</span><strong>{descriptor.consumerName}（{descriptor.consumerId}）尚未实现运行时</strong><p>可以保留目标类型，但不会借用 HomeKit 配对字段或消费端属性目录。</p></div>}
		<label className="enable-row"><input type="checkbox" checked={form.enabled} onChange={(e) => update('enabled', e.target.checked)} />启用此目标实例</label>
		{error && <p className="inline-error">{error}</p>}
		<div className="config-note"><span>消费端设备配置</span><strong>保存目标实例后单独配置</strong><p>目标配置只管理适配器运行参数；消费端设备及其统一模型属性映射从目标卡片进入。</p></div>
		<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '保存中…' : '保存到数据库'}</button></div>
	  </form>
	</div>
}
