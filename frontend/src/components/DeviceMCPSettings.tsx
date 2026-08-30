import { useEffect, useMemo, useState } from 'react'
import { deleteDeviceMCPPropertyConfig, getDeviceMCPConfig, listDeviceMCPPropertyConfigs, saveDeviceMCPConfig, saveDeviceMCPPropertyConfig } from '../api/devices'
import type { Device, MCPAccess, MCPDeviceConfig, MCPPropertyConfig } from '../types/device'
import { HelpTooltip } from './HelpTooltip'

type PropertyTarget = Pick<MCPPropertyConfig, 'endpointId' | 'capabilityId' | 'propertyId'> & { name: string; writable: boolean }

const defaultConfig = (deviceId: string): MCPDeviceConfig => ({ deviceId, enabled: false, usageNote: '', defaultAccess: 'hidden' })
const configKey = (property: Pick<MCPPropertyConfig, 'endpointId' | 'capabilityId' | 'propertyId'>) => `${property.endpointId}\u0000${property.capabilityId}\u0000${property.propertyId}`
const accessLabel: Record<MCPAccess, string> = { hidden: '隐藏', read: '只读', confirm: 'AI 提议，人工确认后执行', inherit: '继承设备默认' }

function propertiesOf(device: Device): PropertyTarget[] {
  return (device.endpoints ?? []).flatMap((endpoint) => (endpoint.capabilities ?? []).flatMap((capability) => (capability.properties ?? []).map((property) => ({
    endpointId: endpoint.id, capabilityId: capability.id, propertyId: property.definition.id, name: property.definition.name, writable: property.definition.writable,
  })))).sort((left, right) => configKey(left).localeCompare(configKey(right)))
}

export function DeviceMCPSettings({ device }: { device: Device }) {
  const [config, setConfig] = useState<MCPDeviceConfig>(() => defaultConfig(device.id))
  const [propertyConfigs, setPropertyConfigs] = useState<Record<string, MCPPropertyConfig>>({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const properties = useMemo(() => propertiesOf(device), [device])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    setConfig(defaultConfig(device.id))
    setPropertyConfigs({})
    void Promise.all([getDeviceMCPConfig(device.id, controller.signal), listDeviceMCPPropertyConfigs(device.id, controller.signal)]).then(([deviceConfig, configs]) => {
      setConfig(deviceConfig)
      setPropertyConfigs(Object.fromEntries(configs.map((item) => [configKey(item), item])))
    }).catch((cause) => {
      if (!(cause instanceof DOMException && cause.name === 'AbortError')) setError(cause instanceof Error ? cause.message : '读取 MCP 配置失败')
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [device.id])

  const saveDevice = async () => {
    setSaving('device')
    setError(null)
    try {
      const saved = await saveDeviceMCPConfig(device.id, { enabled: config.enabled, usageNote: config.usageNote, defaultAccess: config.defaultAccess })
      setConfig(saved)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存 MCP 设备配置失败')
    } finally {
      setSaving(null)
    }
  }

  const saveProperty = async (property: PropertyTarget) => {
    const key = configKey(property)
    const next = propertyConfigs[key] ?? { deviceId: device.id, ...property, usageNote: '', access: 'inherit' as const, allowUnattendedAi: false }
    setSaving(key)
    setError(null)
    try {
      if (next.access === 'inherit' && next.usageNote.trim() === '' && !next.allowUnattendedAi) {
        await deleteDeviceMCPPropertyConfig(device.id, property.endpointId, property.capabilityId, property.propertyId)
        setPropertyConfigs((current) => { const updated = { ...current }; delete updated[key]; return updated })
      } else {
        const saved = await saveDeviceMCPPropertyConfig(device.id, next)
        setPropertyConfigs((current) => ({ ...current, [key]: saved }))
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存 MCP 属性配置失败')
    } finally {
      setSaving(null)
    }
  }

  const updateProperty = (property: PropertyTarget, update: Partial<Pick<MCPPropertyConfig, 'access' | 'usageNote' | 'allowUnattendedAi'>>) => {
    const key = configKey(property)
    setPropertyConfigs((current) => {
      const existing = current[key]
      const next = existing ? { ...existing, ...update } : { deviceId: device.id, endpointId: property.endpointId, capabilityId: property.capabilityId, propertyId: property.propertyId, usageNote: '', access: 'inherit' as const, allowUnattendedAi: false, ...update }
      return { ...current, [key]: next }
    })
  }

  return <details className="mcp-settings"><summary><HelpTooltip label="AI 授权说明" content="默认不开放。选择“确认后执行”时，AI 只能提出操作，仍需人工批准。">AI 授权</HelpTooltip></summary>
    {error && <p className="inline-error" role="alert">{error}</p>}
    <section className="mcp-device-settings" aria-label="设备 MCP 配置">
      <label className="mcp-enabled"><input aria-label="启用该设备的 MCP" type="checkbox" checked={config.enabled} disabled={loading || saving !== null} onChange={(event) => setConfig((current) => ({ ...current, enabled: event.target.checked }))} />开放给 AI</label>
      <label>默认权限<select aria-label="设备默认 MCP 权限" value={config.defaultAccess} disabled={loading || saving !== null} onChange={(event) => setConfig((current) => ({ ...current, defaultAccess: event.target.value as MCPDeviceConfig['defaultAccess'] }))}><option value="hidden">隐藏</option><option value="read">只读</option><option value="confirm">需确认</option></select></label>
      <label className="wide">使用说明<textarea aria-label="设备 MCP 使用备注" maxLength={1000} value={config.usageNote} disabled={loading || saving !== null} onChange={(event) => setConfig((current) => ({ ...current, usageNote: event.target.value }))} placeholder="例如：夜间不要执行。" /></label>
      <button type="button" className="primary" disabled={loading || saving !== null} onClick={() => void saveDevice()}>{saving === 'device' ? '保存中…' : '保存设备'}</button>
    </section>
    <section className="mcp-property-settings" aria-label="属性 MCP 配置">
      <h3><HelpTooltip label="属性权限说明" content="属性权限可覆盖默认权限。只读属性不会被写入。">属性</HelpTooltip></h3>
      {properties.length === 0 ? <p>该设备没有可配置属性。</p> : properties.map((property) => {
        const key = configKey(property)
        const item = propertyConfigs[key] ?? { deviceId: device.id, ...property, usageNote: '', access: 'inherit' as const, allowUnattendedAi: false }
        const effectiveAccess = item.access === 'inherit' ? config.defaultAccess : item.access
        return <article key={key}>
          <header><strong>{property.name}</strong><code>{property.endpointId}.{property.capabilityId}.{property.propertyId}</code>{!property.writable && <span>只读属性</span>}</header>
          <label>属性权限<select aria-label={`${property.name} MCP 权限`} value={item.access} disabled={loading || saving !== null} onChange={(event) => { const access = event.target.value as MCPAccess; const nextEffectiveAccess = access === 'inherit' ? config.defaultAccess : access; updateProperty(property, { access, ...(nextEffectiveAccess !== 'confirm' ? { allowUnattendedAi: false } : {}) }) }}><option value="inherit">{accessLabel.inherit}</option><option value="hidden">{accessLabel.hidden}</option><option value="read">{accessLabel.read}</option><option value="confirm">{accessLabel.confirm}</option></select></label>
          <label className="mcp-enabled"><input aria-label={`${property.name} 允许无人值守 AI 执行`} type="checkbox" checked={item.allowUnattendedAi} disabled={loading || saving !== null || !property.writable || effectiveAccess !== 'confirm'} onChange={(event) => updateProperty(property, { allowUnattendedAi: event.target.checked })} /><HelpTooltip label={`${property.name} 自动执行说明`} content="开启后，已配置为自动运行的任务可直接执行此属性；否则只能提出操作计划。">允许自动任务执行</HelpTooltip></label>
          <label>使用说明<textarea aria-label={`${property.name} MCP 使用备注`} maxLength={1000} value={item.usageNote} disabled={loading || saving !== null} onChange={(event) => updateProperty(property, { usageNote: event.target.value })} placeholder="告诉 AI 使用条件或限制。" /></label>
          <button type="button" disabled={loading || saving !== null} onClick={() => void saveProperty(property)}>{saving === key ? '保存中…' : item.access === 'inherit' && item.usageNote.trim() === '' && !item.allowUnattendedAi ? '清除覆盖' : '保存属性'}</button>
        </article>
      })}
    </section>
  </details>
}
