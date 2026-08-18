import { requestData, requestFile, type DownloadedFile } from './client'

export interface PendingRestore {
	staged: boolean
	requiresRestart: boolean
	createdAt: string
	schemaVersion: number
}

export interface MasterKeyStatus {
	activeVersion: number
	retainedVersions: number[]
	ciphertextsByVersion: Record<string, number>
	legacyCiphertexts: number
	needsReencryption: boolean
}

export interface MasterKeyRotation {
	previousVersion: number
	activeVersion: number
	reencrypted: number
	status: MasterKeyStatus
}

export function downloadDatabaseBackup(confirmation: string): Promise<DownloadedFile> {
	return requestFile('/api/v1/system/backup', { method: 'POST', body: JSON.stringify({ confirmation }) })
}

export function stageDatabaseRestore(file: File, confirmation: string): Promise<PendingRestore> {
	const form = new FormData()
	form.set('file', file)
	form.set('confirmation', confirmation)
	return requestData<PendingRestore>('/api/v1/system/restore', { method: 'POST', body: form })
}

export function getMasterKeyStatus(signal?: AbortSignal): Promise<MasterKeyStatus> {
	return requestData<MasterKeyStatus>('/api/v1/system/master-key', { signal })
}

export function rotateMasterKey(confirmation: string, resume = false): Promise<MasterKeyRotation> {
	return requestData<MasterKeyRotation>('/api/v1/system/master-key/rotate', { method: 'POST', body: JSON.stringify({ confirmation, resume }) })
}
