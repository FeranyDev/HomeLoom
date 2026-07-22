import { describe, expect, it } from 'vitest'
import { deviceLocationLabel, homeLocationKey, homeLocationOptions, matchesDeviceLocation, roomLocationKey, roomLocationOptions, unassignedLocation } from './deviceLocation'

const devices = [
	{ homeId: 'home-a', homeName: '我的家', roomId: 'living', roomName: '客厅' },
	{ homeId: 'home-a', homeName: '我的家', roomId: 'bedroom', roomName: '卧室' },
	{ homeId: 'home-b', homeName: '父母家', roomId: 'living', roomName: '客厅' },
	{},
]

describe('device location filters', () => {
	it('builds stable home and room options without merging same-name rooms across homes', () => {
		expect(homeLocationOptions(devices)).toEqual(expect.arrayContaining([
			{ value: 'id:home-a', label: '我的家' },
			{ value: unassignedLocation, label: '未提供家庭' },
		]))
		expect(roomLocationOptions(devices).filter((item) => item.label.endsWith('/ 客厅'))).toHaveLength(2)
		expect(roomLocationOptions(devices, 'id:home-a')).toEqual(expect.arrayContaining([
			{ value: 'id:home-a::id:living', label: '客厅' },
			{ value: 'id:home-a::id:bedroom', label: '卧室' },
		]))
	})

	it('matches combined filters and gives missing locations explicit labels', () => {
		expect(matchesDeviceLocation(devices[0], homeLocationKey(devices[0]), roomLocationKey(devices[0]))).toBe(true)
		expect(matchesDeviceLocation(devices[1], homeLocationKey(devices[0]), roomLocationKey(devices[0]))).toBe(false)
		expect(deviceLocationLabel({})).toBe('未提供位置')
	})
})
