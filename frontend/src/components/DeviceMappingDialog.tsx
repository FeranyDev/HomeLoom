import { useEffect } from 'react'
import type { Device } from '../types/device'
import { BindingManager } from './BindingManager'

export function DeviceMappingDialog({ device, onClose }: { device: Device; onClose: () => void }) {
  useEffect(() => {
    const close = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    window.addEventListener('keydown', close)
    return () => window.removeEventListener('keydown', close)
  }, [onClose])

  return <div className="modal-backdrop is-mapping is-centered"><section className="device-mapping-dialog" role="dialog" aria-modal="true" aria-label={`${device.name}映射配置`}>
    <div className="form-heading"><div><p className="eyebrow">设备映射（DEVICE MAPPING）</p><h2>{device.name}</h2><small>{device.providerId} / {device.id} · {device.type}</small></div><button onClick={onClose}>关闭</button></div>
    <BindingManager device={device} providerOnly />
  </section></div>
}
