import { requestData } from './client'

export interface XiaomiOAuthStartInput {
	clientId: string
	region: string
	redirectUrl: string
	oauthUuid?: string
	virtualDid?: string
}

export interface XiaomiOAuthStartResult {
	authorizationUrl: string
	state: string
	oauthUuid: string
	virtualDid: string
}

export interface XiaomiOAuthProvisionResult {
	oauth: Record<string, unknown>
	clientId: string
	caCertificate: string
	clientCertificate: string
	privateKey: string
}

export interface XiaomiGateway {
	instance: string
	hostName: string
	addresses: string[]
	port: number
	did?: string
	groupId?: string
	role?: number
	mqttEnabled: boolean
}

export interface XiaomiHubDevice {
	did: string
	name: string
	model?: string
	roomId?: string
	roomName?: string
	specType?: string
	online?: boolean
}

export async function startXiaomiOAuth(input: XiaomiOAuthStartInput): Promise<XiaomiOAuthStartResult> {
	return requestData<XiaomiOAuthStartResult>('/api/v1/xiaomi/oauth/start', { method: 'POST', body: JSON.stringify(input) })
}

export async function completeXiaomiOAuth(input: XiaomiOAuthStartInput & { code: string; state: string }): Promise<XiaomiOAuthProvisionResult> {
	return requestData<XiaomiOAuthProvisionResult>('/api/v1/xiaomi/oauth/complete', { method: 'POST', body: JSON.stringify(input) })
}

export async function discoverXiaomiGateways(): Promise<XiaomiGateway[]> {
	return requestData<XiaomiGateway[]>('/api/v1/xiaomi/gateways')
}

export async function discoverXiaomiDevices(providerId: string): Promise<XiaomiHubDevice[]> {
	return requestData<XiaomiHubDevice[]>(`/api/v1/xiaomi/providers/${encodeURIComponent(providerId)}/devices`)
}
