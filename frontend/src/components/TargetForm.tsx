import { useState } from 'react'
import type { AppleHAPTargetInput, MatterTargetInput, Target, TargetInput, TargetType } from '../types/target'
import { ApiError } from '../api/client'
import { targetTypeLabel } from '../presentationLabels'

interface Props {
	target: Target | null
	onCancel: () => void
	onSave: (input: TargetInput, editing: boolean) => Promise<void>
}

function integerOrAuto(value: string): number | null {
	if (value.trim() === '') return null
	const parsed = Number(value)
	return Number.isFinite(parsed) ? parsed : null
}

function appleInput(target: Target | null): AppleHAPTargetInput {
	return {
		id: target?.id ?? '', type: 'apple-hap', name: target?.name ?? '', enabled: target?.enabled ?? true,
		config: target?.type === 'apple-hap'
			? { address: target.config.address ?? '', pin: target.pairing.pairingCode?.replaceAll('-', '') ?? '', setupId: target.config.setupId ?? '' }
			: { address: '', pin: '', setupId: '' },
		deviceIds: target?.deviceIds ?? [], devices: target?.devices ?? [],
	}
}

function matterInput(target: Target | null): MatterTargetInput {
	const config = target?.type === 'matter' ? target.config : undefined
	return {
		id: target?.id ?? '', type: 'matter', name: target?.name ?? '', enabled: target?.enabled ?? true,
		config: {
			networkInterface: config?.networkInterface ?? '', udpPort: config?.udpPort ?? null, discriminator: config?.discriminator ?? null,
			passcode: null, vendorId: config?.vendorId ?? null, productId: config?.productId ?? null,
			productName: config?.productName ?? '', serialNumber: config?.serialNumber ?? '',
			commissioningWindowSeconds: config?.commissioningWindowSeconds ?? null,
		},
		deviceIds: target?.deviceIds ?? [], devices: target?.devices ?? [],
	}
}

function initialInput(target: Target | null): TargetInput { return target?.type === 'matter' ? matterInput(target) : appleInput(target) }

export function TargetForm({ target, onCancel, onSave }: Props) {
	const editing = target !== null
	const [form, setForm] = useState<TargetInput>(() => initialInput(target))
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
	const pairingLocked = editing && target?.type === 'apple-hap' && target.pairing.paired
	const fieldError = (name: string) => fieldErrors[name] ?? fieldErrors[`config.${name}`] ?? fieldErrors[`matterConfig.${name}`] ?? fieldErrors[`homeKitConfig.${name}`]
	const updateBase = (key: 'id' | 'name' | 'enabled', value: string | boolean) => setForm((current) => ({ ...current, [key]: value } as TargetInput))
	const updateApple = (key: keyof AppleHAPTargetInput['config'], value: string) => setForm((current) => current.type === 'apple-hap' ? { ...current, config: { ...current.config, [key]: value } } : current)
	const updateMatter = (key: keyof MatterTargetInput['config'], value: string | number | null) => setForm((current) => current.type === 'matter' ? { ...current, config: { ...current.config, [key]: value } } : current)
	const changeType = (type: TargetType) => setForm((current) => {
		const next = type === 'matter' ? matterInput(null) : appleInput(null)
		return { ...next, id: current.id, name: current.name, enabled: current.enabled, deviceIds: current.deviceIds, devices: current.devices }
	})

	return <div className="modal-backdrop" role="presentation">
	  <form className="target-form" role="dialog" aria-modal="true" aria-labelledby="target-form-title" onSubmit={(event) => {
		event.preventDefault(); setSaving(true); setError(null); setFieldErrors({})
		void onSave(form, editing).catch((cause) => {
			setError(cause instanceof Error ? cause.message : '保存失败')
			if (cause instanceof ApiError) setFieldErrors(cause.fields)
		}).finally(() => setSaving(false))
	  }}>
		<div className="form-heading"><div><p className="eyebrow">目标实例配置（TARGET CONFIG）</p><h2 id="target-form-title">{editing ? '编辑目标实例' : '新建目标实例'}</h2></div><button type="button" onClick={onCancel}>关闭</button></div>
		<div className="form-grid">
		  <label>目标实例标识（ID）<input aria-invalid={Boolean(fieldError('id'))} value={form.id} disabled={editing} placeholder="留空自动生成" onChange={(event) => updateBase('id', event.target.value)} />{fieldError('id') && <small className="field-error">{fieldError('id')}</small>}</label>
		  <label>目标类型（type）<select aria-invalid={Boolean(fieldError('type'))} value={form.type} disabled={editing} onChange={(event) => changeType(event.target.value as TargetType)}><option value="apple-hap">{targetTypeLabel('apple-hap')}</option><option value="matter">{targetTypeLabel('matter')}</option></select>{fieldError('type') && <small className="field-error">{fieldError('type')}</small>}</label>
		  <label className="wide">名称<input aria-invalid={Boolean(fieldError('name'))} value={form.name} placeholder="留空自动生成目标名称" onChange={(event) => updateBase('name', event.target.value)} />{fieldError('name') && <small className="field-error">{fieldError('name')}</small>}</label>
		  {form.type === 'apple-hap' ? <>
			<label>HAP 监听地址（address）<input aria-invalid={Boolean(fieldError('address'))} value={form.config.address} placeholder="留空自动分配端口" onChange={(event) => updateApple('address', event.target.value)} />{fieldError('address') && <small className="field-error">{fieldError('address')}</small>}</label>
			{!pairingLocked && <><label>HomeKit 设置标识（Setup ID）<input aria-invalid={Boolean(fieldError('setupId'))} value={form.config.setupId} maxLength={4} placeholder="留空自动生成" onChange={(event) => updateApple('setupId', event.target.value.toUpperCase())} />{fieldError('setupId') && <small className="field-error">{fieldError('setupId')}</small>}</label><label>HomeKit 8 位配对码（PIN）<input aria-invalid={Boolean(fieldError('pin'))} value={form.config.pin} maxLength={8} placeholder="留空自动生成" onChange={(event) => updateApple('pin', event.target.value)} />{fieldError('pin') && <small className="field-error">{fieldError('pin')}</small>}</label></>}
		  </> : <>
			<label>网络接口（networkInterface）<input aria-invalid={Boolean(fieldError('networkInterface'))} value={form.config.networkInterface} placeholder="留空自动选择活跃接口" onChange={(event) => updateMatter('networkInterface', event.target.value)} />{fieldError('networkInterface') && <small className="field-error">{fieldError('networkInterface')}</small>}</label>
			<label>UDP 监听端口（udpPort）<input aria-invalid={Boolean(fieldError('udpPort'))} type="number" min="1" max="65535" value={form.config.udpPort ?? ''} placeholder="留空自动分配" onChange={(event) => updateMatter('udpPort', integerOrAuto(event.target.value))} />{fieldError('udpPort') && <small className="field-error">{fieldError('udpPort')}</small>}</label>
			<label>配网辨识码（discriminator）<input aria-invalid={Boolean(fieldError('discriminator'))} type="number" min="0" max="4095" value={form.config.discriminator ?? ''} placeholder="留空自动生成" onChange={(event) => updateMatter('discriminator', integerOrAuto(event.target.value))} />{fieldError('discriminator') && <small className="field-error">{fieldError('discriminator')}</small>}</label>
			<label>配网密码（passcode）<input aria-invalid={Boolean(fieldError('passcode'))} inputMode="numeric" value={form.config.passcode ?? ''} placeholder={editing ? '留空保持现有密码' : '留空自动生成'} onChange={(event) => updateMatter('passcode', event.target.value || null)} />{fieldError('passcode') && <small className="field-error">{fieldError('passcode')}</small>}</label>
			<label>厂商 ID（VID）<input aria-invalid={Boolean(fieldError('vendorId'))} type="number" min="0" max="65535" value={form.config.vendorId ?? ''} placeholder="留空自动生成测试 VID" onChange={(event) => updateMatter('vendorId', integerOrAuto(event.target.value))} />{fieldError('vendorId') && <small className="field-error">{fieldError('vendorId')}</small>}</label>
			<label>产品 ID（PID）<input aria-invalid={Boolean(fieldError('productId'))} type="number" min="0" max="65535" value={form.config.productId ?? ''} placeholder="留空自动生成测试 PID" onChange={(event) => updateMatter('productId', integerOrAuto(event.target.value))} />{fieldError('productId') && <small className="field-error">{fieldError('productId')}</small>}</label>
			<label>产品名（productName）<input aria-invalid={Boolean(fieldError('productName'))} value={form.config.productName} placeholder="留空使用 HomeLoom Matter Bridge" onChange={(event) => updateMatter('productName', event.target.value)} />{fieldError('productName') && <small className="field-error">{fieldError('productName')}</small>}</label>
			<label>序列号（serialNumber）<input aria-invalid={Boolean(fieldError('serialNumber'))} value={form.config.serialNumber} placeholder="留空自动生成" onChange={(event) => updateMatter('serialNumber', event.target.value)} />{fieldError('serialNumber') && <small className="field-error">{fieldError('serialNumber')}</small>}</label>
			<label className="wide">默认配网窗口时长（commissioningWindowSeconds）<input aria-invalid={Boolean(fieldError('commissioningWindowSeconds'))} type="number" min="1" max="86400" value={form.config.commissioningWindowSeconds ?? ''} placeholder="留空采用运行时默认值" onChange={(event) => updateMatter('commissioningWindowSeconds', integerOrAuto(event.target.value))} />{fieldError('commissioningWindowSeconds') && <small className="field-error">{fieldError('commissioningWindowSeconds')}</small>}</label>
		  </>}
		</div>
		{pairingLocked && <div className="config-note pairing-locked-note"><span>HomeKit 已配对</span><strong>一次性配对参数已隐藏并锁定</strong><p>PIN 和 Setup ID 在已加入 Apple Home 后不再用于日常运行。如需重新配对，请先在桥卡片上清除配对身份。</p></div>}
		{form.type === 'matter' && <div className="config-note matter-config-note"><span>Matter 自动值</span><strong>留空不是 HomeKit 回退</strong><p>端口、discriminator、passcode 与测试 VID/PID 留空时由 Matter Runtime 独立生成；保存后可在桥卡片查看非敏感运行状态。测试 Vendor/Product 不代表 CSA 认证设备。</p></div>}
		<label className="enable-row"><input type="checkbox" checked={form.enabled} onChange={(event) => updateBase('enabled', event.target.checked)} />启用此目标实例</label>
		{error && <p className="inline-error">{error}</p>}
		<div className="config-note"><span>消费端设备配置</span><strong>保存目标实例后单独配置</strong><p>目标配置只管理适配器运行参数；消费端设备及其统一模型属性映射从目标卡片进入。</p></div>
		<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '保存中…' : '保存到数据库'}</button></div>
	  </form>
	</div>
}
