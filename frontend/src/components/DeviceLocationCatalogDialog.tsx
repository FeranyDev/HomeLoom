import { useState } from 'react'
import { createDeviceLocationHome, createDeviceLocationRoom, deleteDeviceLocationHome, deleteDeviceLocationRoom, listDeviceLocations, updateDeviceLocationHome, updateDeviceLocationRoom } from '../api/devices'
import type { DeviceLocationHome } from '../types/device'
import { HelpTooltip } from './HelpTooltip'

export function DeviceLocationCatalogDialog({ homes, onChange, onClose }: {
	homes: DeviceLocationHome[]
	onChange: (homes: DeviceLocationHome[]) => void
	onClose: () => void
}) {
	const [newHome, setNewHome] = useState('')
	const [homeNames, setHomeNames] = useState<Record<string, string>>({})
	const [roomNames, setRoomNames] = useState<Record<string, string>>({})
	const [newRooms, setNewRooms] = useState<Record<string, string>>({})
	const [busy, setBusy] = useState('')
	const [error, setError] = useState<string | null>(null)

	async function mutate(key: string, action: () => Promise<unknown>) {
		setBusy(key); setError(null)
		try {
			await action()
			onChange(await listDeviceLocations())
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '更新家庭与房间失败')
		} finally { setBusy('') }
	}

	return <div className="modal-backdrop" role="presentation">
		<section className="location-catalog-dialog" role="dialog" aria-modal="true" aria-labelledby="location-catalog-title">
			<header><div><p className="eyebrow">HOMELOOM LOCATION DIRECTORY</p><h3 id="location-catalog-title"><HelpTooltip content="这里维护 HomeLoom 的统一位置；来源原始 ID 会保留。" label="位置目录说明">家庭与房间</HelpTooltip></h3></div><button type="button" className="location-catalog-close" aria-label="关闭" onClick={onClose}><span aria-hidden="true">×</span></button></header>
			<form className="location-catalog-create" onSubmit={(event) => { event.preventDefault(); const name = newHome.trim(); if (!name) return; void mutate('new-home', async () => { await createDeviceLocationHome(name); setNewHome('') }) }}>
				<label>新家庭<input aria-label="新家庭名称" value={newHome} onChange={(event) => setNewHome(event.target.value)} placeholder="例如：我的家" /></label>
				<button className="primary" disabled={!newHome.trim() || Boolean(busy)}>添加家庭</button>
			</form>
			{error && <p className="inline-error" role="alert">{error}</p>}
			<div className="location-catalog-list">
				{homes.map((home) => {
					const homeName = homeNames[home.id] ?? home.name
					const newRoom = newRooms[home.id] ?? ''
					return <article key={home.id}>
						<div className="location-catalog-home">
							<label>家庭名称<input aria-label={`${home.name} 家庭名称`} value={homeName} onChange={(event) => setHomeNames((current) => ({ ...current, [home.id]: event.target.value }))} /></label>
							<button type="button" disabled={!homeName.trim() || homeName.trim() === home.name || Boolean(busy)} onClick={() => void mutate(`home-${home.id}`, () => updateDeviceLocationHome(home.id, homeName.trim()))}>保存名称</button>
							<button type="button" className="is-danger" disabled={Boolean(busy)} onClick={() => void mutate(`delete-home-${home.id}`, () => deleteDeviceLocationHome(home.id))}>删除家庭</button>
						</div>
						<div className="location-catalog-rooms">
							{home.rooms.map((room) => {
								const roomName = roomNames[room.id] ?? room.name
								return <div key={room.id}><label>房间<input aria-label={`${home.name} ${room.name} 房间名称`} value={roomName} onChange={(event) => setRoomNames((current) => ({ ...current, [room.id]: event.target.value }))} /></label><button type="button" disabled={!roomName.trim() || roomName.trim() === room.name || Boolean(busy)} onClick={() => void mutate(`room-${room.id}`, () => updateDeviceLocationRoom(home.id, room.id, roomName.trim()))}>保存</button><button type="button" className="is-danger" disabled={Boolean(busy)} onClick={() => void mutate(`delete-room-${room.id}`, () => deleteDeviceLocationRoom(home.id, room.id))}>删除</button></div>
							})}
							<form onSubmit={(event) => { event.preventDefault(); const name = newRoom.trim(); if (!name) return; void mutate(`new-room-${home.id}`, async () => { await createDeviceLocationRoom(home.id, name); setNewRooms((current) => ({ ...current, [home.id]: '' })) }) }}><label>新房间<input aria-label={`${home.name} 新房间名称`} value={newRoom} onChange={(event) => setNewRooms((current) => ({ ...current, [home.id]: event.target.value }))} placeholder="例如：客厅" /></label><button className="primary" disabled={!newRoom.trim() || Boolean(busy)}>添加房间</button></form>
						</div>
					</article>
				})}
				{homes.length === 0 && <p className="location-catalog-empty"><HelpTooltip content="创建后，可在设备位置中选择它。" label="创建家庭说明">暂无家庭</HelpTooltip></p>}
			</div>
		</section>
	</div>
}
