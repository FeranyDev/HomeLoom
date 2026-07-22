export interface LocatedDevice {
	homeId?: string
	homeName?: string
	roomId?: string
	roomName?: string
}

export interface LocationOption { value: string; label: string }

export const unassignedLocation = '__unassigned__'

function identity(id?: string, name?: string): string {
	const normalizedID = id?.trim()
	if (normalizedID) return `id:${normalizedID}`
	const normalizedName = name?.trim()
	return normalizedName ? `name:${normalizedName}` : unassignedLocation
}

export function homeLocationKey(item: LocatedDevice): string {
	return identity(item.homeId, item.homeName)
}

export function roomLocationKey(item: LocatedDevice): string {
	return `${homeLocationKey(item)}::${identity(item.roomId, item.roomName)}`
}

export function matchesDeviceLocation(item: LocatedDevice, home: string, room: string): boolean {
	return (!home || homeLocationKey(item) === home) && (!room || roomLocationKey(item) === room)
}

export function homeLocationOptions(items: LocatedDevice[]): LocationOption[] {
	const options = new Map<string, string>()
	for (const item of items) {
		const value = homeLocationKey(item)
		options.set(value, value === unassignedLocation ? '未提供家庭' : item.homeName?.trim() || item.homeId?.trim() || '未提供家庭')
	}
	return Array.from(options, ([value, label]) => ({ value, label })).sort((left, right) => left.label.localeCompare(right.label, 'zh-CN'))
}

export function roomLocationOptions(items: LocatedDevice[], home = ''): LocationOption[] {
	const options = new Map<string, string>()
	for (const item of items) {
		if (home && homeLocationKey(item) !== home) continue
		const value = roomLocationKey(item)
		const room = item.roomName?.trim() || item.roomId?.trim() || '未分配房间'
		const homeLabel = item.homeName?.trim() || item.homeId?.trim() || '未提供家庭'
		options.set(value, home ? room : `${homeLabel} / ${room}`)
	}
	return Array.from(options, ([value, label]) => ({ value, label })).sort((left, right) => left.label.localeCompare(right.label, 'zh-CN'))
}

export function deviceLocationLabel(item: LocatedDevice): string {
	const home = item.homeName?.trim() || item.homeId?.trim()
	const room = item.roomName?.trim() || item.roomId?.trim()
	return home || room ? `${home || '未提供家庭'} / ${room || '未分配房间'}` : '未提供位置'
}
