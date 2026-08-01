import { isMatterTargetInput, isMatterTargetType } from '../types/target'
import type { AppleHAPTarget, HomeKitCameraTarget, MatterCommissioning, MatterTarget, MatterTargetConfig, Target, TargetInput, TargetStatus, TargetType, TargetVirtualDevice } from '../types/target'
import { requestData, requestJSON } from './client'

type RecordValue = Record<string, unknown>

function record(value: unknown): RecordValue { return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {} }
function string(value: unknown): string | undefined { return typeof value === 'string' ? value : undefined }
function boolean(value: unknown): boolean | undefined { return typeof value === 'boolean' ? value : undefined }
function number(value: unknown): number | undefined { return typeof value === 'number' && Number.isFinite(value) ? value : undefined }
function status(value: unknown): TargetStatus { return value === 'disabled' || value === 'starting' || value === 'running' || value === 'error' ? value : 'error' }
function targetDevices(value: unknown): TargetVirtualDevice[] { return Array.isArray(value) ? value as TargetVirtualDevice[] : [] }
function ids(value: unknown): string[] { return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [] }

/**
 * API compatibility boundary. The canonical browser model is a discriminated
 * protocol union. During rollout this accepts the old flat HAP response, so a
 * mixed-version deployment cannot accidentally make Matter render HAP fields.
 */
export function normalizeTarget(value: unknown): Target {
	const raw = record(value)
	const type: TargetType = raw.type === 'matter' || raw.type === 'matter-camera'
		? raw.type
		: raw.type === 'homekit-camera' ? 'homekit-camera' : 'apple-hap'
	const issues = Array.isArray(raw.issues)
		? raw.issues.map((item) => {
			const issue = record(item)
			return {
				deviceId: string(issue.deviceId),
				deviceName: string(issue.deviceName),
				deviceType: string(issue.deviceType),
				stage: string(issue.stage) ?? 'unknown',
				message: string(issue.message) ?? '',
			}
		}).filter((item) => item.message)
		: undefined
	const diagnosticsRaw = record(raw.diagnostics)
	const diagnostics = Object.keys(diagnosticsRaw).length > 0
		? Object.fromEntries(Object.entries(diagnosticsRaw).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
		: undefined
	const common = {
		id: string(raw.id) ?? '', type, consumerId: string(raw.consumerId), name: string(raw.name) ?? '', enabled: boolean(raw.enabled) ?? false,
		status: status(raw.status), deviceIds: ids(raw.deviceIds), devices: targetDevices(raw.devices), error: string(raw.error), issues, diagnostics, removed: boolean(raw.removed) ?? false,
	}
	const config = record(raw.config)
	if (!isMatterTargetType(type)) {
		const pairing = record(raw.pairing)
		return {
			...common, type,
			config: { address: string(config.address) ?? string(record(raw.homeKitConfig).address) ?? string(raw.address), setupId: string(config.setupId) ?? string(record(raw.homeKitConfig).setupId) ?? string(raw.setupId) },
			pairing: {
				paired: boolean(pairing.paired) ?? boolean(raw.paired) ?? false,
				pairingCode: string(pairing.pairingCode) ?? string(raw.pairingCode),
				setupUri: string(pairing.setupUri) ?? string(raw.setupUri),
			},
		} satisfies AppleHAPTarget | HomeKitCameraTarget
	}
	const commissioningRaw = record(raw.commissioning)
	const windowOpen = boolean(commissioningRaw.windowOpen) ?? boolean(raw.commissioningWindowOpen) ?? false
	const state = string(commissioningRaw.state) ?? string(raw.commissioningState)
	const commissioning: MatterCommissioning = {
		state: state === 'uncommissioned' || state === 'window-open' || state === 'commissioned' ? state : windowOpen ? 'window-open' : 'unknown',
		windowOpen,
		windowExpiresAt: string(commissioningRaw.windowExpiresAt) ?? string(raw.commissioningWindowExpiresAt),
		manualPairingCode: string(commissioningRaw.manualPairingCode) ?? string(raw.manualPairingCode) ?? string(raw.pairingCode),
		setupPayload: string(commissioningRaw.setupPayload) ?? string(raw.setupPayload) ?? string(raw.setupUri),
	}
	const runtime = record(raw.runtime)
	const matterRaw = Object.keys(config).length > 0 ? config : record(raw.matterConfig)
	const matterConfig: MatterTargetConfig = {
		networkInterface: string(matterRaw.networkInterface) ?? string(raw.networkInterface),
		udpPort: number(matterRaw.udpPort) ?? number(raw.udpPort),
		discriminator: number(matterRaw.discriminator) ?? number(raw.discriminator),
		vendorId: number(matterRaw.vendorId) ?? number(raw.vendorId),
		productId: number(matterRaw.productId) ?? number(raw.productId),
		productName: string(matterRaw.productName) ?? string(raw.productName),
		serialNumber: string(matterRaw.serialNumber) ?? string(raw.serialNumber),
		commissioningWindowSeconds: number(matterRaw.commissioningWindowSeconds) ?? number(raw.commissioningWindowSeconds),
		protocolVersion: string(matterRaw.protocolVersion) ?? string(raw.protocolVersion) ?? string(runtime.protocolVersion),
	}
	return {
		...common, type, config: matterConfig, commissioning,
		fabricCount: number(raw.fabricCount) ?? (Array.isArray(raw.fabrics) ? raw.fabrics.length : 0),
		fabrics: Array.isArray(raw.fabrics) ? raw.fabrics.map((item) => {
			const fabric = record(item)
			return { id: string(fabric.id) ?? '', label: string(fabric.label) }
		}) : undefined,
		endpointCount: number(raw.endpointCount) ?? 0,
		runtime: string(runtime.interface) || string(runtime.protocolVersion) || string(raw.networkInterface) || string(raw.protocolVersion)
			? { interface: string(runtime.interface) ?? string(raw.networkInterface), protocolVersion: string(runtime.protocolVersion) ?? string(raw.protocolVersion) }
			: undefined,
		certification: raw.certification === 'test' || raw.certification === 'certified' ? raw.certification : 'unknown',
	} satisfies MatterTarget
}

function requestPayload(input: TargetInput): RecordValue {
	if (isMatterTargetInput(input)) {
		const { config, ...common } = input
		return { ...common, matterConfig: config }
	}
	// Current stable servers still read flat HAP keys. Keeping this conversion at
	// the boundary avoids widening the Matter branch with those keys.
	return { ...input, homeKitConfig: input.config, address: input.config.address, pin: input.config.pin, setupId: input.config.setupId }
}

export async function saveTarget(input: TargetInput, editing: boolean): Promise<Target> {
	const path = editing ? `/api/v1/targets/${encodeURIComponent(input.id)}` : '/api/v1/targets'
	return normalizeTarget(await requestData<unknown>(path, {
		method: editing ? 'PUT' : 'POST',
		body: JSON.stringify(requestPayload(input)),
	}))
}

export async function deleteTarget(id: string): Promise<void> {
	await requestJSON<void>(`/api/v1/targets/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function regenerateTargetPairing(id: string, confirmation: string): Promise<Target> {
	return requestData<unknown>(`/api/v1/targets/${encodeURIComponent(id)}/pairing/regenerate`, { method: 'POST', body: JSON.stringify({ confirmation }) }).then(normalizeTarget)
}

export function clearTargetPairingIdentity(id: string, confirmation: string): Promise<Target> {
	return requestData<unknown>(`/api/v1/targets/${encodeURIComponent(id)}/pairing-identity`, { method: 'DELETE', body: JSON.stringify({ confirmation }) }).then(normalizeTarget)
}

export function openMatterCommissioningWindow(id: string, durationSeconds: number | undefined, confirmation: string): Promise<Target> {
	return requestData<unknown>(`/api/v1/targets/${encodeURIComponent(id)}/commissioning-window`, { method: 'POST', body: JSON.stringify({ durationSeconds, confirmation }) }).then(normalizeTarget)
}

export function closeMatterCommissioningWindow(id: string): Promise<Target> {
	return requestData<unknown>(`/api/v1/targets/${encodeURIComponent(id)}/commissioning-window`, { method: 'DELETE' }).then(normalizeTarget)
}

export function deleteMatterFabric(id: string, fabricID: string, confirmation: string): Promise<Target> {
	return requestData<unknown>(`/api/v1/targets/${encodeURIComponent(id)}/fabrics/${encodeURIComponent(fabricID)}`, { method: 'DELETE', body: JSON.stringify({ confirmation }) }).then(normalizeTarget)
}

export function factoryResetMatterTarget(id: string, confirmation: string): Promise<Target> {
	return requestData<unknown>(`/api/v1/targets/${encodeURIComponent(id)}/factory-reset`, { method: 'POST', body: JSON.stringify({ confirmation }) }).then(normalizeTarget)
}

export function confirmMatterEndpointDeviceType(id: string, consumerDeviceID: string, deviceType: string, confirmation: string): Promise<Target> {
	return requestData<unknown>(`/api/v1/targets/${encodeURIComponent(id)}/endpoints/${encodeURIComponent(consumerDeviceID)}/device-type`, {
		method: 'POST', body: JSON.stringify({ deviceType, confirmation }),
	}).then(normalizeTarget)
}

export async function listTargets(signal?: AbortSignal): Promise<Target[]> {
	const items = await requestData<unknown[]>('/api/v1/targets', { signal })
	return items.map(normalizeTarget)
}

export function pairingQRUrl(id: string): string {
	return `/api/v1/targets/${encodeURIComponent(id)}/pairing-qr`
}

export function matterQRUrl(id: string): string {
	return `/api/v1/targets/${encodeURIComponent(id)}/commissioning-qr`
}
