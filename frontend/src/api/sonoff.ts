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

export function loginSonoff(input: SonoffLoginInput): Promise<SonoffLoginResult> {
	return requestData<SonoffLoginResult>('/api/v1/sonoff/login', { method: 'POST', body: JSON.stringify(input) })
}
