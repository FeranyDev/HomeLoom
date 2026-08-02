import { ApiError, requestData } from './client'
import type { Provider, ProviderAuthChallenge } from '../types/provider'

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
	gatewayAvailable?: boolean
	localControlAvailable?: boolean
	cloudAvailable?: boolean
	pushAvailable?: boolean
	specType?: string
	online?: boolean
}

export interface XiaomiCloudLoginResult extends Partial<ProviderAuthChallenge> {
	status: string
	challengeId?: string
	verificationUrl?: string
	expiresAt?: string
	userId?: string
	ssecurity?: string
	serviceToken?: string
	passToken?: string
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

/**
 * Reads the challenge retained by an already configured Provider. Runtime
 * startup can encounter identity verification independently of the explicit
 * account-login form, so this endpoint is intentionally separate from
 * `/login/start` and does not require sending the account password again.
 */
export async function getXiaomiProviderAuthChallenge(providerId: string): Promise<XiaomiCloudLoginResult | null> {
	const encoded = encodeURIComponent(providerId)
	const xiaomiPath = `/api/v1/xiaomi-miot-cloud/providers/${encoded}/auth-challenge`
	try {
		return await requestData<XiaomiCloudLoginResult | null>(xiaomiPath)
	} catch (cause) {
		if (!(cause instanceof ApiError) || (cause.status !== 404 && cause.status !== 405)) throw cause
		return requestData<XiaomiCloudLoginResult | null>(`/api/v1/providers/${encoded}/auth-challenge`)
	}
}

/**
 * Completes a runtime Provider challenge. The backend persists the resulting
 * session and returns the refreshed Provider snapshot (secrets remain
 * redacted by the normal Provider API contract).
 */
export async function verifyXiaomiProviderAuthChallenge(providerId: string, input: { challengeId: string; code: string }): Promise<Provider> {
	const base = `/api/v1/xiaomi-miot-cloud/providers/${encodeURIComponent(providerId)}/auth-challenge`
	try {
		return await requestData<Provider>(`${base}/verify`, { method: 'POST', body: JSON.stringify(input) })
	} catch (cause) {
		// Older backend builds mounted POST on the challenge resource itself;
		// retain a narrow fallback while deployments roll forward to the
		// explicit `/verify` action route.
		if (!(cause instanceof ApiError) || (cause.status !== 404 && cause.status !== 405)) throw cause
		return requestData<Provider>(base, { method: 'POST', body: JSON.stringify(input) })
	}
}

export async function discoverXiaomiGateways(): Promise<XiaomiGateway[]> {
	return requestData<XiaomiGateway[]>('/api/v1/xiaomi/gateways')
}

export async function discoverXiaomiDevices(providerId: string, providerType = 'xiaomi'): Promise<XiaomiHubDevice[]> {
	const base = providerType === 'xiaomi-miot-cloud' ? 'xiaomi-miot-cloud' : 'xiaomi'
	return requestData<XiaomiHubDevice[]>(`/api/v1/${base}/providers/${encodeURIComponent(providerId)}/devices`)
}
