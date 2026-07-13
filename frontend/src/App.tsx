import { useCallback, useEffect, useRef, useState } from 'react'
import { executeDeviceCommand, listDevices, setDeviceEnabled, setDevicePower, setDeviceProperty, simulateDevice, subscribeDevices } from './api/devices'
import { deleteTarget, listTargets, saveTarget, subscribeTargets } from './api/targets'
import { deleteProvider, listProviders, restartProvider, saveProvider, testProviderConnection } from './api/providers'
import { getDiagnostics, getRuntimeSettings, listAuditEvents, listCommands, saveRuntimeSettings, subscribeAuditEvents, subscribeCommands } from './api/diagnostics'
import { getSystemVersion } from './api/system'
import { DeviceCard } from './components/DeviceCard'
import { TargetCard } from './components/TargetCard'
import { TargetForm } from './components/TargetForm'
import { ProviderCard } from './components/ProviderCard'
import { ProviderForm } from './components/ProviderForm'
import { SystemDashboard } from './components/SystemDashboard'
import { MappingWorkspace } from './components/MappingWorkspace'
import { DeviceDetails } from './components/DeviceDetails'
import { ToastCenter } from './components/ToastCenter'
import { useToasts } from './useToasts'
import { CollectionEmpty, LoadingState } from './components/PageState'
import type { Device, DeviceAvailability, PropertyValue } from './types/device'
import type { Target, TargetInput } from './types/target'
import type { Provider, ProviderInput } from './types/provider'
import type { AuditEvent, DeviceCommand, Diagnostics, RuntimeSettings, SystemVersion } from './types/diagnostics'
import { usePageRoute } from './routing'
import { confirmProviderDeletion, confirmTargetDeletion } from './confirmations'

export function App() {
  const [devices, setDevices] = useState<Device[]>([])
	const [targets, setTargets] = useState<Target[]>([])
	const [providers, setProviders] = useState<Provider[]>([])
	const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null)
	const [version, setVersion] = useState<SystemVersion | null>(null)
	const [commands, setCommands] = useState<DeviceCommand[]>([])
	const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
	const [runtimeSettings, setRuntimeSettings] = useState<RuntimeSettings | null>(null)
	const commandHistoryLimit = useRef(1000)
	const [page, setPage] = usePageRoute()
	const [targetForm, setTargetForm] = useState<{ open: boolean, target: Target | null }>({ open: false, target: null })
	const [providerForm, setProviderForm] = useState<{ open: boolean, provider: Provider | null }>({ open: false, provider: null })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set())
  const [live, setLive] = useState(false)
  const [deviceQuery, setDeviceQuery] = useState('')
  const [deviceStatus, setDeviceStatus] = useState<'all' | 'online' | 'offline' | 'unknown' | 'disabled' | 'removed'>('all')
  const [selectedDeviceID, setSelectedDeviceID] = useState<string | null>(null)
  const { toasts, notify, dismiss } = useToasts()

  const refresh = useCallback(async (signal?: AbortSignal) => {
    try {
	  const [deviceData, targetData, providerData, diagnosticData, commandData, auditData, versionData, settingsData] = await Promise.all([listDevices(signal), listTargets(signal), listProviders(signal), getDiagnostics(signal), listCommands(signal), listAuditEvents(signal).catch(() => []), getSystemVersion(signal).catch(() => null), getRuntimeSettings(signal).catch(() => null)])
	  setDevices(deviceData)
	  setTargets(targetData)
	  setProviders(providerData)
	  setDiagnostics(diagnosticData)
	  if (settingsData) commandHistoryLimit.current = settingsData.commandHistoryLimit
	  setCommands(commandData.slice(0, commandHistoryLimit.current))
	  setAuditEvents(auditData)
	  setVersion(versionData)
	  setRuntimeSettings(settingsData)
      setError(null)
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') return
      setError(cause instanceof Error ? cause.message : '无法连接后端')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void refresh(controller.signal)
    const timer = window.setInterval(() => void refresh(), 30000)
    const unsubscribe = subscribeDevices((updated) => setDevices((current) => { const exists = current.some((item) => item.id === updated.id); return exists ? current.map((item) => item.id === updated.id ? updated : item) : [...current, updated] }), setLive)
	const unsubscribeCommands = subscribeCommands((updated) => setCommands((current) => { const exists = current.some((item) => item.id === updated.id); const next = exists ? current.map((item) => item.id === updated.id ? updated : item) : [updated, ...current]; return next.sort((left, right) => new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime()).slice(0, commandHistoryLimit.current) }))
	const unsubscribeAudit = subscribeAuditEvents((updated) => setAuditEvents((current) => [updated, ...current.filter((item) => item.id !== updated.id)].slice(0, 200)))
	const unsubscribeTargets = subscribeTargets((updated) => setTargets((current) => current.map((item) => item.id === updated.id ? updated : item)))
    return () => {
      controller.abort()
      window.clearInterval(timer)
      unsubscribe()
	  unsubscribeCommands()
	  unsubscribeAudit()
	  unsubscribeTargets()
    }
  }, [refresh])

  async function handlePowerChange(device: Device, value: boolean) {
    setPendingIds((current) => new Set(current).add(device.id))
    try {
      const updated = await setDevicePower(device.id, value)
      setDevices((current) => current.map((item) => item.id === updated.id ? updated : item))
      setError(null)
	  notify('success', `${device.name}已${value ? '开启' : '关闭'}`)
    } catch (cause) {
      notify('error', cause instanceof Error ? cause.message : '控制设备失败')
    } finally {
	  try { const [diagnosticData, commandData] = await Promise.all([getDiagnostics(), listCommands()]); setDiagnostics(diagnosticData); setCommands(commandData) } catch { /* periodic refresh will reconcile diagnostics */ }
      setPendingIds((current) => {
        const next = new Set(current)
        next.delete(device.id)
        return next
      })
    }
  }

	async function handleTargetSave(input: TargetInput, editing: boolean) {
		try {
			await saveTarget(input, editing)
			setTargetForm({ open: false, target: null })
			await refresh()
			setError(null)
			notify('success', editing ? '桥配置已更新并实时应用' : '桥已创建并启动')
		} catch (cause) {
			notify('error', cause instanceof Error ? cause.message : '保存桥失败')
			throw cause
		}
	}

	async function handleTargetDelete(target: Target) {
		if (!confirmTargetDeletion(target.name)) return
		try { await deleteTarget(target.id); await refresh(); notify('success', `桥“${target.name}”已删除`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '删除桥失败') }
	}
	async function handleProviderSave(input: ProviderInput, editing: boolean) { try { await saveProvider(input, editing); setProviderForm({ open: false, provider: null }); await refresh(); notify('success', editing ? 'Provider 配置已更新' : 'Provider 已创建') } catch (cause) { notify('error', cause instanceof Error ? cause.message : '保存 Provider 失败'); throw cause } }
	async function handleProviderTest(input: ProviderInput) { try { await testProviderConnection(input); notify('success', 'Provider 连接测试成功') } catch (cause) { notify('error', cause instanceof Error ? cause.message : 'Provider 连接测试失败'); throw cause } }
	async function handleProviderDelete(provider: Provider) { if (!confirmProviderDeletion(provider.name)) return; try { await deleteProvider(provider.id); await refresh(); notify('success', `Provider“${provider.name}”已删除`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '删除 Provider 失败') } }
	async function handleProviderRestart(provider: Provider) { try { await restartProvider(provider.id); await refresh(); setError(null); notify('success', `Provider“${provider.name}”已重新启动`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : 'Provider 重新启动失败'); throw cause } }
	async function handleSimulation(device: Device, values: { availability?: DeviceAvailability; online?: boolean; power?: boolean; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; sequence?: number; repeat?: number }) { try { const updated = await simulateDevice(device.id, values); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); setError(null); notify('info', `${device.name}模拟状态已更新`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '模拟状态失败'); throw cause } }
	async function handleDeviceEnabled(device: Device, enabled: boolean) { setPendingIds((current) => new Set(current).add(device.id)); try { const updated = await setDeviceEnabled(device.id, enabled); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); notify('success', `${device.name}已${enabled ? '启用' : '禁用'}`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '设备状态更新失败') } finally { setPendingIds((current) => { const next = new Set(current); next.delete(device.id); return next }) } }
	async function handlePropertyWrite(device: Device, endpointId: string, capabilityId: string, propertyId: string, value: PropertyValue) { try { const updated = await setDeviceProperty(device.id, endpointId, capabilityId, propertyId, value); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); const [diagnosticData, commandData] = await Promise.all([getDiagnostics(), listCommands()]); setDiagnostics(diagnosticData); setCommands(commandData); setError(null); notify('success', `${device.name}.${propertyId} 写入成功`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '属性写入失败'); throw cause } }
	async function handleCommandExecute(device: Device, endpointId: string, capabilityId: string, commandId: string, parameters: Record<string, PropertyValue>, idempotencyKey: string) { try { const updated = await executeDeviceCommand(device.id, endpointId, capabilityId, commandId, parameters, idempotencyKey); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); const [diagnosticData, commandData] = await Promise.all([getDiagnostics(), listCommands()]); setDiagnostics(diagnosticData); setCommands(commandData); setError(null); notify('success', `${device.name}.${commandId} 执行成功`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '命令执行失败'); throw cause } }
	async function handleRuntimeSettingsSave(next: RuntimeSettings) { try { const saved = await saveRuntimeSettings(next); commandHistoryLimit.current = saved.commandHistoryLimit; setCommands((current) => current.slice(0, saved.commandHistoryLimit)); setRuntimeSettings(saved); notify('success', '运行时设置已保存并实时生效') } catch (cause) { notify('error', cause instanceof Error ? cause.message : '保存运行时设置失败'); throw cause } }
	const pageCopy = page === 'devices' ? { title: <>把家的状态<br />织在一起。</>, intro: '设备状态驻留内存，由 Provider 实时上报。', eyebrow: 'DEVICES', section: '设备中心' } : page === 'providers' ? { title: <>数据源，随时<br />接入或离开。</>, intro: 'Provider 配置存储于 SQLite，并可在线启停和替换。', eyebrow: 'PROVIDERS', section: 'Provider 管理' } : page === 'targets' ? { title: <>一座桥，或<br />很多座桥。</>, intro: '按设备或平台拆分桥实例。每座桥拥有独立身份、端口、配对资料和二维码。', eyebrow: 'TARGETS', section: '桥接中心' } : page === 'mapping' ? { title: <>让数据说<br />同一种语言。</>, intro: '在写入 Profile 前预览正向和反向转换的每一步。', eyebrow: 'MAPPING', section: '映射调试' } : { title: <>看见系统的<br />每一次呼吸。</>, intro: '观察事件队列、设备连接和命令生命周期。', eyebrow: 'SYSTEM', section: '系统诊断' }
	const summary = page === 'devices' ? devices.filter((item) => item.availability === 'online').length : page === 'providers' ? providers.filter((item) => item.status === 'running').length : page === 'targets' ? targets.filter((item) => item.status === 'running').length : page === 'mapping' ? 5 : diagnostics?.eventsProcessed ?? 0
	const filteredDevices = devices.filter((item) => { const matchesText = `${item.name} ${item.id} ${item.providerId}`.toLowerCase().includes(deviceQuery.trim().toLowerCase()); const matchesStatus = deviceStatus === 'all' || (deviceStatus === 'disabled' ? item.disabled : deviceStatus === 'removed' ? item.removed : item.availability === deviceStatus && !item.disabled && !item.removed); return matchesText && matchesStatus })
	const selectedDevice = selectedDeviceID ? devices.find((item) => item.id === selectedDeviceID) ?? null : null

  return (<>
	<a className="skip-link" href="#main-content">跳到主要内容</a>
    <main id="main-content" tabIndex={-1}>
	  <nav className="top-nav" aria-label="主要导航">
	    <a className="wordmark" href="#/devices">HomeLoom</a>
	    <div>
	      <button aria-current={page === 'devices' ? 'page' : undefined} className={page === 'devices' ? 'is-active' : ''} onClick={() => setPage('devices')}>设备</button>
	      <button aria-current={page === 'providers' ? 'page' : undefined} className={page === 'providers' ? 'is-active' : ''} onClick={() => setPage('providers')}>Provider</button>
	      <button aria-current={page === 'targets' ? 'page' : undefined} className={page === 'targets' ? 'is-active' : ''} onClick={() => setPage('targets')}>桥接中心</button>
	      <button aria-current={page === 'mapping' ? 'page' : undefined} className={page === 'mapping' ? 'is-active' : ''} onClick={() => setPage('mapping')}>映射</button>
	      <button aria-current={page === 'system' ? 'page' : undefined} className={page === 'system' ? 'is-active' : ''} onClick={() => setPage('system')}>系统</button>
	    </div>
	    <span className="runtime-meta"><span className="version-badge" title={version ? `${version.commit} · ${version.buildTime}` : '版本读取中'}>{version?.version ?? '…'}</span><span aria-live="polite" className={`live-indicator ${live ? 'is-live' : ''}`}>{live ? '实时' : '重连中'}</span></span>
	  </nav>
      <header className="hero">
        <div>
          <p className="eyebrow">HOMELOOM · DEMO 01</p>
		  <h1>{pageCopy.title}</h1><p className="intro">{pageCopy.intro}</p>
        </div>
        <div className="summary">
		  <span>{summary}</span><small>{page === 'devices' ? '在线设备' : page === 'providers' ? '运行中 Provider' : page === 'targets' ? '运行中的桥' : page === 'mapping' ? '转换能力' : '已处理事件'}</small>
        </div>
      </header>

      <section className="section-heading">
        <div>
		  <p className="eyebrow">{pageCopy.eyebrow}</p><h2>{pageCopy.section}</h2>
        </div>
		<div className="heading-actions">{page === 'providers' && <button className="add-button" onClick={() => setProviderForm({ open: true, provider: null })}>＋ 新建 Provider</button>}{page === 'targets' && <button className="add-button" onClick={() => setTargetForm({ open: true, target: null })}>＋ 新建桥</button>}<button className="refresh-button" onClick={() => void refresh()} disabled={loading}>刷新状态</button></div>
      </section>
	  {page === 'devices' && <div className="device-filters"><input aria-label="搜索设备" value={deviceQuery} onChange={(event) => setDeviceQuery(event.target.value)} placeholder="搜索名称、ID 或 Provider" /><select aria-label="设备状态" value={deviceStatus} onChange={(event) => setDeviceStatus(event.target.value as typeof deviceStatus)}><option value="all">全部状态</option><option value="online">仅在线</option><option value="offline">暂时离线</option><option value="unknown">可用性未知</option><option value="disabled">人工禁用</option><option value="removed">来源已删除</option></select><span>{filteredDevices.length} / {devices.length}</span></div>}

      {error && <div className="error-banner" role="alert">{error}，请确认后端已在 8090 端口运行。</div>}
      {loading ? (
        <LoadingState />
      ) : (
		page === 'devices' ? <section className="device-grid">
		  {filteredDevices.map((device) => (
            <DeviceCard
              key={device.id}
              device={device}
              pending={pendingIds.has(device.id)}
              onPowerChange={(item, value) => void handlePowerChange(item, value)}
              onDetails={(item) => setSelectedDeviceID(item.id)}
			  onEnabledChange={(item, enabled) => void handleDeviceEnabled(item, enabled)}
            />
          ))}
		  {filteredDevices.length === 0 && <CollectionEmpty title="没有匹配的设备" description={devices.length ? '请调整搜索文字或在线状态筛选。' : '启用 Provider 后，发现的设备会显示在这里。'} />}
		</section> : page === 'providers' ? <section className="provider-grid"><div className="config-note"><span>配置来源</span><strong>SQLite · providers</strong><p>保存后运行时立即应用；单个 Provider 失败不会影响其他实例，可独立重新启动。</p></div>{providers.map((provider) => <ProviderCard key={provider.id} provider={provider} devices={devices.filter((item) => item.providerId === provider.id && !item.removed)} onEdit={(item) => setProviderForm({ open: true, provider: item })} onDelete={(item) => void handleProviderDelete(item)} onRestart={handleProviderRestart} onSimulate={handleSimulation} />)}{providers.length === 0 && <CollectionEmpty title="还没有 Provider" description="创建 Provider 后，HomeLoom 会立即初始化并发现设备。" />}</section> : page === 'mapping' ? <MappingWorkspace devices={devices} /> : page === 'system' ? <SystemDashboard diagnostics={diagnostics} commands={commands} auditEvents={auditEvents} settings={runtimeSettings} onSettingsSave={handleRuntimeSettingsSave} /> : <section className="target-list">
		  <div className="config-note">
		    <span>配置来源</span>
		    <strong>SQLite · targets</strong>
		    <p>桥配置、设备绑定和配对参数统一保存在数据库中；YAML 只负责进程启动。</p>
		  </div>
		  {targets.map((target) => <TargetCard key={target.id} target={target} onEdit={(item) => setTargetForm({ open: true, target: item })} onDelete={(item) => void handleTargetDelete(item)} />)}
		  {targets.length === 0 && <CollectionEmpty title="还没有桥" description="新建桥并绑定设备后，即可接入 HomeKit 等目标平台。" />}
		</section>
      )}
	  {targetForm.open && <TargetForm target={targetForm.target} devices={devices.filter((item) => !item.removed)} onCancel={() => setTargetForm({ open: false, target: null })} onSave={handleTargetSave} />}
	  {providerForm.open && <ProviderForm provider={providerForm.provider} onCancel={() => setProviderForm({ open: false, provider: null })} onSave={handleProviderSave} onTest={handleProviderTest} />}
	  {selectedDevice && <DeviceDetails device={selectedDevice} onClose={() => setSelectedDeviceID(null)} onPropertyWrite={(endpointId, capabilityId, propertyId, value) => handlePropertyWrite(selectedDevice, endpointId, capabilityId, propertyId, value)} onCommandExecute={(endpointId, capabilityId, commandId, parameters, idempotencyKey) => handleCommandExecute(selectedDevice, endpointId, capabilityId, commandId, parameters, idempotencyKey)} />}
	  <ToastCenter toasts={toasts} dismiss={dismiss} />
    </main>
	</>)
}
