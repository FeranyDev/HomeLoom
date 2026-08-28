import { requestData } from './client'

export interface SonoffLoginInput {
	username: string
	password: string
	countryCode?: string
	region?: string
	endpoint?: string
	appId?: string
	appSecret?: string
}

export interface SonoffLoginResult {
	accessToken: string
	region: string
	endpoint: string
}

export interface SonoffDirectoryDevice {
	id: string
	deviceId: string
	name: string
	model?: string
	uiid: number
	type?: string
	homeId?: string
	homeName?: string
	roomId?: string
	roomName?: string
	channels: number
	online: boolean
	configured: boolean
}

export function loginSonoff(input: SonoffLoginInput): Promise<SonoffLoginResult> {
	return requestData<SonoffLoginResult>('/api/v1/sonoff/login', { method: 'POST', body: JSON.stringify(input) })
}

export function discoverSonoffDevices(providerId: string): Promise<SonoffDirectoryDevice[]> {
	return requestData<SonoffDirectoryDevice[]>(`/api/v1/sonoff/providers/${encodeURIComponent(providerId)}/devices`)
}
