import { requestData } from './client'

export interface TuyaOAuthStartInput {
	accessId: string
	accessSecret: string
	region?: string
	baseUrl?: string
	authorizationUrl: string
	redirectUrl: string
}

export interface TuyaOAuthStartResult {
	authorizationUrl: string
	state: string
	expiresAt: string
}

export interface TuyaOAuthCompleteResult {
	accessToken: string
	refreshToken: string
	uid: string
	expiresAt: string
}

export interface TuyaOAuthCallbackMessage {
	type: 'homeloom-tuya-oauth'
	code?: string
	state?: string
	error?: string
}

export interface TuyaSharingLoginStartResult {
	state: string
	qrData: string
	expiresAt: string
}

export interface TuyaSharingLoginPollResult {
	status: 'pending' | 'complete' | 'expired' | string
	message?: string
	accessToken?: string
	refreshToken?: string
	uid?: string
	endpoint?: string
	terminalId?: string
	expiresAt?: string
}

export interface TuyaDirectoryDevice {
	id: string
	deviceId: string
	name: string
	type: string
	category?: string
	productId?: string
	productName?: string
	model?: string
	homeId?: string
	homeName?: string
	roomId?: string
	roomName?: string
	online: boolean
	configured: boolean
	specification?: Record<string, unknown>
	status?: Array<Record<string, unknown>>
}

export function startTuyaSharingLogin(userCode: string): Promise<TuyaSharingLoginStartResult> {
	return requestData<TuyaSharingLoginStartResult>('/api/v1/tuya/login/start', { method: 'POST', body: JSON.stringify({ userCode }) })
}

export function pollTuyaSharingLogin(state: string): Promise<TuyaSharingLoginPollResult> {
	return requestData<TuyaSharingLoginPollResult>('/api/v1/tuya/login/poll', { method: 'POST', body: JSON.stringify({ state }) })
}

export function discoverTuyaDevices(providerId: string): Promise<TuyaDirectoryDevice[]> {
	return requestData<TuyaDirectoryDevice[]>(`/api/v1/tuya/providers/${encodeURIComponent(providerId)}/devices`)
}

export function tuyaSharingQRCodeURL(state: string): string {
	return `/api/v1/tuya/login/qr?state=${encodeURIComponent(state)}`
}

export function startTuyaOAuth(input: TuyaOAuthStartInput): Promise<TuyaOAuthStartResult> {
	return requestData<TuyaOAuthStartResult>('/api/v1/tuya/oauth/start', { method: 'POST', body: JSON.stringify(input) })
}

export function completeTuyaOAuth(input: { state: string; code: string }): Promise<TuyaOAuthCompleteResult> {
	return requestData<TuyaOAuthCompleteResult>('/api/v1/tuya/oauth/complete', { method: 'POST', body: JSON.stringify(input) })
}

export function tuyaOAuthQRCodeURL(state: string): string {
	return `/api/v1/tuya/oauth/qr?state=${encodeURIComponent(state)}`
}

export function parseTuyaOAuthCallback(value: unknown): TuyaOAuthCallbackMessage | null {
	if (!value || typeof value !== 'object' || Array.isArray(value)) return null
	const raw = value as Record<string, unknown>
	if (raw.type !== 'homeloom-tuya-oauth') return null
	return {
		type: 'homeloom-tuya-oauth',
		...(typeof raw.code === 'string' ? { code: raw.code } : {}),
		...(typeof raw.state === 'string' ? { state: raw.state } : {}),
		...(typeof raw.error === 'string' ? { error: raw.error } : {}),
	}
}
