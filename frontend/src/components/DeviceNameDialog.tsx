import { useState } from 'react'
import type { Device } from '../types/device'

export function DeviceNameDialog({ device, onCancel, onSave, onReset }: {
	device: Device
	onCancel: () => void
	onSave: (name: string) => Promise<void>
	onReset: () => Promise<void>
}) {
	const [name, setName] = useState(device.name)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const sourceName = device.sourceName || (device.nameOverridden ? '当前设备源未提供名称' : device.name)

	async function save() {
		if (!name.trim()) { setError('请输入设备名称'); return }
		setSaving(true); setError(null)
		try { await onSave(name.trim()) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存设备名称失败') } finally { setSaving(false) }
	}

	async function reset() {
		setSaving(true); setError(null)
		try { await onReset() } catch (cause) { setError(cause instanceof Error ? cause.message : '恢复设备源名称失败') } finally { setSaving(false) }
	}

	return <div className="modal-backdrop" role="presentation">
		<section className="device-location-dialog" role="dialog" aria-modal="true" aria-labelledby="device-name-title">
			<header><div><p className="eyebrow">UNIFIED DEVICE NAME</p><h3 id="device-name-title">{device.name} · HomeLoom 名称</h3><p>此名称由 HomeLoom 统一保存，适用于所有设备来源与已连接的控制端；提供端刷新不会覆盖它。</p></div></header>
			<div className="device-location-source"><span>设备源名称</span><strong>{sourceName}</strong></div>
			<label>显示名称<input aria-label="设备显示名称" maxLength={128} value={name} onChange={(event) => setName(event.target.value)} autoFocus /></label>
			{device.nameOverridden && <p className="form-hint">当前正在使用 HomeLoom 自定义名称。恢复后会采用最新的设备源名称。</p>}
			{error && <p className="inline-error" role="alert">{error}</p>}
			<footer><button type="button" onClick={onCancel}>取消</button>{device.nameOverridden && <button type="button" disabled={saving} onClick={() => void reset()}>恢复来源名称</button>}<button type="button" className="primary" disabled={saving || !name.trim()} onClick={() => void save()}>{saving ? '正在保存…' : '保存名称'}</button></footer>
		</section>
	</div>
}
