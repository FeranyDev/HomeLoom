import { useMemo, useState } from 'react'
import type { Device, DeviceLocationHome, DeviceLocationMode } from '../types/device'
import { deviceLocationLabel } from '../deviceLocation'

export interface DeviceLocationValue {
	mode: DeviceLocationMode
	homeId?: string
	roomId?: string
}

export function DeviceLocationDialog({ device, homes, onCancel, onManage, onSave }: {
	device: Device
	homes: DeviceLocationHome[]
	onCancel: () => void
	onManage: () => void
	onSave: (value: DeviceLocationValue) => Promise<void>
}) {
	const initialMode = device.locationMode === 'custom' ? 'custom' : 'source'
	const [mode, setMode] = useState<DeviceLocationMode>(initialMode)
	const [homeId, setHomeId] = useState(initialMode === 'custom' ? device.homeId ?? '' : '')
	const [roomId, setRoomId] = useState(initialMode === 'custom' ? device.roomId ?? '' : '')
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const selectedHome = useMemo(() => homes.find((home) => home.id === homeId), [homes, homeId])

	async function submit() {
		if (mode === 'custom' && !selectedHome) { setError('请先选择一个已配置的家庭'); return }
		setSaving(true); setError(null)
		try {
			await onSave(mode === 'source' ? { mode } : { mode, homeId: selectedHome!.id, ...(roomId ? { roomId } : {}) })
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '保存设备位置失败')
		} finally { setSaving(false) }
	}

	return <div className="modal-backdrop" role="presentation">
		<section className="device-location-dialog" role="dialog" aria-modal="true" aria-labelledby="device-location-title">
			<header><div><p className="eyebrow">UNIFIED DEVICE LOCATION</p><h3 id="device-location-title">{device.name} · HomeLoom 位置</h3><p>设备源位置保持只读；同名来源会自动归入 HomeLoom 位置，也可以为设备明确选择其他家庭与房间。</p></div></header>
			<div className="device-location-source"><span>设备源位置</span><strong>{deviceLocationLabel({ homeId: device.sourceHomeId, homeName: device.sourceHomeName, roomId: device.sourceRoomId, roomName: device.sourceRoomName })}</strong></div>
			<label>位置策略<select aria-label="位置策略" value={mode} onChange={(event) => setMode(event.target.value as DeviceLocationMode)}><option value="source">继承设备源位置</option><option value="custom">选择 HomeLoom 位置</option></select></label>
			{mode === 'custom' && <div className="device-location-custom">
				<label>家庭<select aria-label="HomeLoom 家庭" value={homeId} onChange={(event) => { setHomeId(event.target.value); setRoomId('') }}><option value="">请选择家庭</option>{homes.map((home) => <option value={home.id} key={home.id}>{home.name}</option>)}</select></label>
				<label>房间<select aria-label="HomeLoom 房间" value={roomId} disabled={!selectedHome} onChange={(event) => setRoomId(event.target.value)}><option value="">不指定房间</option>{selectedHome?.rooms.map((room) => <option value={room.id} key={room.id}>{room.name}</option>)}</select></label>
				<div className="device-location-manage"><span>{homes.length ? '选项来自 HomeLoom 位置配置。' : '还没有可选家庭，请先创建。'}</span><button type="button" onClick={onManage}>管理家庭与房间</button></div>
			</div>}
			{error && <p className="inline-error" role="alert">{error}</p>}
			<footer><button type="button" onClick={onCancel}>取消</button><button type="button" className="primary" disabled={saving || (mode === 'custom' && !selectedHome)} onClick={() => void submit()}>{saving ? '正在保存…' : '保存位置'}</button></footer>
		</section>
	</div>
}
