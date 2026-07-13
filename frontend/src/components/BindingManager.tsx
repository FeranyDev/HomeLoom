import { useCallback, useEffect, useMemo, useState } from 'react'
import { createMappingBinding, deleteMappingBinding, listMappingBindings, listMappingProfiles, updateMappingBinding } from '../api/mapping'
import type { Device, Property, ValueType } from '../types/device'
import type { MappingBinding, MappingProfileInfo } from '../types/mapping'

interface BindingAPI {
  listBindings: () => Promise<MappingBinding[]>
  listProfiles: () => Promise<MappingProfileInfo[]>
  create: (binding: Omit<MappingBinding, 'id'> & { id?: string }) => Promise<MappingBinding>
  update: (id: string, binding: MappingBinding) => Promise<MappingBinding>
  remove: (id: string) => Promise<void>
}

const defaultAPI: BindingAPI = { listBindings: listMappingBindings, listProfiles: listMappingProfiles, create: createMappingBinding, update: updateMappingBinding, remove: deleteMappingBinding }
type PropertyOption = { key: string; endpointId: string; capabilityId: string; propertyId: string; property: Property }

function propertiesOf(device?: Device): PropertyOption[] {
  if (!device) return []
  return device.endpoints.flatMap((endpoint) => endpoint.capabilities.flatMap((capability) => capability.properties.map((property) => ({ key: `${endpoint.id}\u0000${capability.id}\u0000${property.definition.id}`, endpointId: endpoint.id, capabilityId: capability.id, propertyId: property.definition.id, property }))))
}

function profileCompatible(profile: MappingProfileInfo, type?: ValueType): boolean {
  return profile.kind !== 'target' && profile.inputType === type && profile.outputType === type && !profile.transforms.some((item) => item.type === 'clamp')
}

function errorText(cause: unknown): string { return cause instanceof Error ? cause.message : '映射绑定操作失败' }

export function BindingManager({ devices, profileRevision = 0, api = defaultAPI }: { devices: Device[]; profileRevision?: number; api?: BindingAPI }) {
  const [bindings, setBindings] = useState<MappingBinding[]>([])
  const [profiles, setProfiles] = useState<MappingProfileInfo[]>([])
  const [deviceId, setDeviceId] = useState('')
  const [propertyKey, setPropertyKey] = useState('')
  const [profileId, setProfileId] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
	const selectableDevices = useMemo(() => devices.filter((item) => !item.removed), [devices])
	const selectedDevice = useMemo(() => selectableDevices.find((item) => item.id === deviceId), [deviceId, selectableDevices])
  const propertyOptions = useMemo(() => propertiesOf(selectedDevice), [selectedDevice])
	const selectedProperty = useMemo(() => propertyOptions.find((item) => item.key === propertyKey), [propertyKey, propertyOptions])
	const compatibleProfiles = useMemo(() => profiles.filter((item) => profileCompatible(item, selectedProperty?.property.definition.type)), [profiles, selectedProperty])

  const refresh = useCallback(async () => {
    try {
      const [nextBindings, nextProfiles] = await Promise.all([api.listBindings(), api.listProfiles()])
      setBindings(nextBindings); setProfiles(nextProfiles); setError(null)
    } catch (cause) { setError(errorText(cause)) }
  }, [api])
  useEffect(() => { void refresh() }, [refresh, profileRevision])
  useEffect(() => { if (selectableDevices.length && !selectableDevices.some((item) => item.id === deviceId)) setDeviceId(selectableDevices[0].id) }, [deviceId, selectableDevices])
  useEffect(() => { setPropertyKey(propertyOptions[0]?.key ?? '') }, [propertyOptions])
  useEffect(() => { if (!compatibleProfiles.some((item) => item.id === profileId)) setProfileId(compatibleProfiles[0]?.id ?? '') }, [compatibleProfiles, profileId])

  const create = async () => {
    if (!selectedDevice || !selectedProperty || !profileId) { setError('请选择设备属性和兼容的 Profile'); return }
    setSaving(true); setError(null)
    try {
      await api.create({ profileId, providerId: selectedDevice.providerId, deviceId: selectedDevice.id, endpointId: selectedProperty.endpointId, capabilityId: selectedProperty.capabilityId, propertyId: selectedProperty.propertyId, enabled: true })
      await refresh()
    } catch (cause) { setError(errorText(cause)) } finally { setSaving(false) }
  }
  const toggle = async (item: MappingBinding) => { try { await api.update(item.id, { ...item, enabled: !item.enabled }); await refresh() } catch (cause) { setError(errorText(cause)) } }
  const remove = async (item: MappingBinding) => { if (!window.confirm(`删除 ${item.deviceId}.${item.propertyId} 的映射绑定？`)) return; try { await api.remove(item.id); await refresh() } catch (cause) { setError(errorText(cause)) } }

  return <section className="binding-manager">
    <div className="profile-heading"><div><p className="eyebrow">LIVE PROPERTY BINDINGS</p><h3>设备属性绑定</h3><p>将 Provider 原始属性绑定到 Profile。事件正向转换、控制反向转换，保存后立即刷新快照，无需重启。</p></div><span>{bindings.filter((item) => item.enabled).length} 个生效中</span></div>
    <div className="binding-editor">
      <label>设备<select aria-label="绑定设备" value={deviceId} onChange={(event) => setDeviceId(event.target.value)}><option value="">选择设备</option>{selectableDevices.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.providerId}</option>)}</select></label>
      <label>属性<select aria-label="绑定属性" value={propertyKey} onChange={(event) => setPropertyKey(event.target.value)}><option value="">选择属性</option>{propertyOptions.map((item) => <option key={item.key} value={item.key}>{item.endpointId}.{item.capabilityId}.{item.propertyId} · {item.property.definition.type}</option>)}</select></label>
      <label>Profile<select aria-label="绑定 Profile" value={profileId} onChange={(event) => setProfileId(event.target.value)}><option value="">选择兼容 Profile</option>{compatibleProfiles.map((item) => <option key={item.id} value={item.id}>{item.id} · v{item.version}</option>)}</select></label>
      <button className="add-button" disabled={saving || !profileId} onClick={() => void create()}>{saving ? '绑定中…' : '＋ 创建并实时应用'}</button>
    </div>
    {error && <p className="field-error" role="alert">{error}</p>}
    <div className="binding-list">{bindings.map((item) => <article key={item.id} className={item.enabled ? '' : 'is-disabled'}><div><strong>{item.deviceId}</strong><code>{item.endpointId}.{item.capabilityId}.{item.propertyId}</code><small>{item.providerId} → {item.profileId}</small></div><div><span>{item.enabled ? '实时生效' : '已停用'}</span><button onClick={() => void toggle(item)}>{item.enabled ? '停用' : '启用'}</button><button className="danger-link" onClick={() => void remove(item)}>删除</button></div></article>)}</div>
    {bindings.length === 0 && <div className="empty-state">还没有绑定。选择设备属性与 Profile 后即可让映射进入真实读写链路。</div>}
  </section>
}
