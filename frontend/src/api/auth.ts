import { requestData, requestJSON } from './client'

export interface AuthStatus {
	initialized: boolean
	authenticated: boolean
	username?: string
}

export function getAuthStatus(signal?: AbortSignal): Promise<AuthStatus> {
	return requestData<AuthStatus>('/api/v1/auth/status', { signal })
}

export function setupAdministrator(username: string, password: string): Promise<AuthStatus> {
	return requestData<AuthStatus>('/api/v1/auth/setup', { method: 'POST', body: JSON.stringify({ username, password }) })
}

export function login(username: string, password: string): Promise<AuthStatus> {
	return requestData<AuthStatus>('/api/v1/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
}

export function logout(): Promise<void> {
	return requestJSON<void>('/api/v1/auth/logout', { method: 'POST' })
}
