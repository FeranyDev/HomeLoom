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
	homeId?: string
	homeName?: string
	roomId?: string
	roomName?: string
	localIp?: string
	localAvailable?: boolean
	specType?: string
	online?: boolean
}

export interface XiaomiCloudLoginResult {
	status: 'verified' | 'verification_required'
	challengeId?: string
	verificationUrl?: string
	expiresAt?: string
	userId?: string
	ssecurity?: string
	serviceToken?: string
}

export async function startXiaomiOAuth(input: XiaomiOAuthStartInput): Promise<XiaomiOAuthStartResult> {
	return requestData<XiaomiOAuthStartResult>('/api/v1/xiaomi/oauth/start', { method: 'POST', body: JSON.stringify(input) })
}

export async function completeXiaomiOAuth(input: XiaomiOAuthStartInput & { code: string; state: string }): Promise<XiaomiOAuthProvisionResult> {
	return requestData<XiaomiOAuthProvisionResult>('/api/v1/xiaomi/oauth/complete', { method: 'POST', body: JSON.stringify(input) })
}

export async function startXiaomiCloudLogin(input: { region: string; username: string; password: string; requestTimeoutSeconds?: number }): Promise<XiaomiCloudLoginResult> {
	return requestData<XiaomiCloudLoginResult>('/api/v1/xiaomi-miot-cloud/login/start', { method: 'POST', body: JSON.stringify(input) })
}

export async function verifyXiaomiCloudLogin(input: { challengeId: string; code: string }): Promise<XiaomiCloudLoginResult> {
	return requestData<XiaomiCloudLoginResult>('/api/v1/xiaomi-miot-cloud/login/verify', { method: 'POST', body: JSON.stringify(input) })
}

export async function discoverXiaomiGateways(): Promise<XiaomiGateway[]> {
	return requestData<XiaomiGateway[]>('/api/v1/xiaomi/gateways')
}

export async function discoverXiaomiDevices(providerId: string, providerType = 'xiaomi'): Promise<XiaomiHubDevice[]> {
	const base = providerType === 'xiaomi-miot-cloud' ? 'xiaomi-miot-cloud' : 'xiaomi'
	return requestData<XiaomiHubDevice[]>(`/api/v1/${base}/providers/${encodeURIComponent(providerId)}/devices`)
}
