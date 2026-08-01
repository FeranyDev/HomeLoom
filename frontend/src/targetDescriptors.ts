import type { TargetType } from './types/target'

export interface TargetDescriptor {
  consumerId: string
  consumerName: string
  supportsHomeKitPairing: boolean
  implemented: boolean
}

const descriptors: Record<TargetType, TargetDescriptor> = {
  'apple-hap': { consumerId: 'homekit', consumerName: 'Apple Home / HomeKit', supportsHomeKitPairing: true, implemented: true },
  'homekit-camera': { consumerId: 'homekit-camera', consumerName: 'Apple Home 独立摄像头', supportsHomeKitPairing: true, implemented: true },
  matter: { consumerId: 'matter', consumerName: 'Matter', supportsHomeKitPairing: false, implemented: true },
  'matter-camera': { consumerId: 'matter-camera', consumerName: 'Matter 独立摄像头', supportsHomeKitPairing: false, implemented: true },
}

export function targetDescriptor(type: TargetType): TargetDescriptor { return descriptors[type] }
