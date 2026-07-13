import { requestData, requestFile, type DownloadedFile } from './client'

export interface PendingRestore {
	staged: boolean
	requiresRestart: boolean
	createdAt: string
	schemaVersion: number
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
