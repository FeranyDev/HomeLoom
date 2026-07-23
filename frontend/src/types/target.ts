export type TargetType = 'apple-hap' | 'matter'
export type TargetStatus = 'disabled' | 'starting' | 'running' | 'error'

export interface TargetVirtualDevice {
	id: string
	name: string
	type: import('./device').DeviceType
	sourceDeviceId: string
	auxiliarySourceDeviceIds?: string[]
	enabled: boolean
}

/** Fields shared by every bridge protocol. Protocol configuration deliberately
 * lives in the discriminated branch below: Matter must never inherit HAP's
 * listener, Setup ID, or PIN fields. */
export interface TargetBase {
	id: string
	type: TargetType
	consumerId?: string
	name: string
	enabled: boolean
	status: TargetStatus
	deviceIds: string[]
	devices: TargetVirtualDevice[]
	error?: string
	/** Present on the SSE tombstone emitted after a target is deleted. */
	removed?: boolean
}

export interface AppleHAPConfig {
	/** Listener address, for example :51826. An empty value asks the service to choose it. */
	address?: string
	setupId?: string
}

export interface AppleHAPPairing {
	paired: boolean
	pairingCode?: string
	setupUri?: string
}

export interface AppleHAPTarget extends TargetBase {
	type: 'apple-hap'
	config: AppleHAPConfig
	pairing: AppleHAPPairing
}

export type MatterCommissioningState = 'uncommissioned' | 'window-open' | 'commissioned' | 'unknown'

export interface MatterCommissioning {
	state: MatterCommissioningState
	windowOpen: boolean
	windowExpiresAt?: string
	/** Returned only while the commissioning window is open. */
	manualPairingCode?: string
	/** A Matter setup payload used to render the QR code. */
	setupPayload?: string
}

export interface MatterFabric {
	id: string
	label?: string
}

export interface MatterTargetConfig {
	networkInterface?: string
	udpPort?: number
	discriminator?: number
	vendorId?: number
	productId?: number
	productName?: string
	serialNumber?: string
	commissioningWindowSeconds?: number
	protocolVersion?: string
}

export interface MatterTarget extends TargetBase {
	type: 'matter'
	config: MatterTargetConfig
	commissioning: MatterCommissioning
	fabricCount: number
	fabrics?: MatterFabric[]
	endpointCount: number
	/** Runtime values are optional while older services are rolling out. */
	runtime?: {
		interface?: string
		protocolVersion?: string
	}
	certification?: 'test' | 'certified' | 'unknown'
}

export type Target = AppleHAPTarget | MatterTarget

export interface TargetInputBase {
	id: string
	type: TargetType
	name: string
	enabled: boolean
	deviceIds: string[]
	devices: TargetVirtualDevice[]
}

export interface AppleHAPTargetInput extends TargetInputBase {
	type: 'apple-hap'
	config: {
		address: string
		/** Empty means automatically generate on creation / retain on edit. */
		pin: string
		setupId: string
	}
}

export interface MatterTargetInput extends TargetInputBase {
	type: 'matter'
	config: {
		/** Empty means the runtime selects a suitable active interface. */
		networkInterface: string
		/** null means automatic allocation/generation; it is intentional JSON, not an omitted HomeKit field. */
		udpPort: number | null
		discriminator: number | null
		passcode: string | null
		vendorId: number | null
		productId: number | null
		productName: string
		serialNumber: string
		commissioningWindowSeconds: number | null
	}
}

export type TargetInput = AppleHAPTargetInput | MatterTargetInput
