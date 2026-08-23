import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { executeDeviceCommand, listDeviceLocations, listDevices, setDeviceEnabled, setDeviceLocation, setDevicePower, setDeviceProperty, simulateDevice } from './api/devices'
import { clearTargetPairingIdentity, closeMatterCommissioningWindow, confirmMatterEndpointDeviceType, deleteMatterFabric, deleteTarget, factoryResetMatterTarget, listTargets, openMatterCommissioningWindow, regenerateTargetPairing, saveTarget } from './api/targets'
import { deleteProvider, listProviders, restartProvider, revokeProviderCredentials, saveProvider, testProviderConnection } from './api/providers'
import { getDiagnostics, getRuntimeSettings, listAuditEvents, listCommands, saveRuntimeSettings } from './api/diagnostics'
import { subscribeEvents } from './api/events'
import { getSystemVersion } from './api/system'
import { DeviceCard } from './components/DeviceCard'
import { TargetCard } from './components/TargetCard'
import { TargetForm } from './components/TargetForm'
import { ToastCenter } from './components/ToastCenter'
import { useToasts } from './useToasts'
import { CollectionEmpty, LoadingState } from './components/PageState'
import type { Device, DeviceAvailability, DeviceLocationHome, PropertyValue } from './types/device'
import type { MatterFabric, MatterTarget, Target, TargetInput, TargetType } from './types/target'
import type { Provider, ProviderInput } from './types/provider'
import type { AuditEvent, DeviceCommand, Diagnostics, RuntimeSettings, SystemVersion } from './types/diagnostics'
import { usePageRoute } from './routing'
import { confirmExactPhrase, confirmProviderDeletion, confirmTargetDeletion } from './confirmations'
import { getAuthStatus, login, logout, setupAdministrator, type AuthStatus } from './api/auth'
import { AuthScreen } from './components/AuthScreen'
import { DeviceLocationDialog, type DeviceLocationValue } from './components/DeviceLocationDialog'
import { DeviceLocationCatalogDialog } from './components/DeviceLocationCatalogDialog'
import { BrandMark } from './components/BrandMark'
import { listModelContracts } from './api/mapping'
import { homeLocationOptions, matchesDeviceLocation, roomLocationOptions } from './deviceLocation'
import { deviceTypeLabel } from './presentationLabels'
import { supportsProviderChildDevices } from './providerRouting'

// 大体积页面与按需对话框通过动态 import 拆包，避免首屏单一超大 chunk（原先 ~588 kB）。
// 轻量组件（DeviceCard / TargetCard / TargetForm / 位置对话框等）保持静态引入，保证首帧与测试同步渲染。
const TargetDeviceManager = lazy(() => import('./components/TargetDeviceManager').then((module) => ({ default: module.TargetDeviceManager })))
const ProviderForm = lazy(() => import('./components/ProviderForm').then((module) => ({ default: module.ProviderForm })))
const SystemDashboard = lazy(() => import('./components/SystemDashboard').then((module) => ({ default: module.SystemDashboard })))
const AIWorkspace = lazy(() => import('./components/AIWorkspace').then((module) => ({ default: module.AIWorkspace })))
const MappingWorkspace = lazy(() => import('./components/MappingWorkspace').then((module) => ({ default: module.MappingWorkspace })))
const ProviderWorkspace = lazy(() => import('./components/ProviderWorkspace').then((module) => ({ default: module.ProviderWorkspace })))
const VirtualDeviceManager = lazy(() => import('./components/VirtualDeviceManager').then((module) => ({ default: module.VirtualDeviceManager })))
const MQTTDeviceManager = lazy(() => import('./components/MQTTDeviceManager').then((module) => ({ default: module.MQTTDeviceManager })))
const CameraDeviceManager = lazy(() => import('./components/CameraDeviceManager').then((module) => ({ default: module.CameraDeviceManager })))
const GreeDeviceManager = lazy(() => import('./components/GreeDeviceManager').then((module) => ({ default: module.GreeDeviceManager })))
const XiaomiDeviceManager = lazy(() => import('./components/XiaomiDeviceManager').then((module) => ({ default: module.XiaomiDeviceManager })))
const DeviceMappingDialog = lazy(() => import('./components/DeviceMappingDialog').then((module) => ({ default: module.DeviceMappingDialog })))
const DeviceDetails = lazy(() => import('./components/DeviceDetails').then((module) => ({ default: module.DeviceDetails })))
const LogicalDeviceManager = lazy(() => import('./components/LogicalDeviceManager').then((module) => ({ default: module.LogicalDeviceManager })))

export function App() {
	const [auth, setAuth] = useState<AuthStatus | null>(null)
	const [authError, setAuthError] = useState<string | null>(null)

	const refreshAuth = useCallback(async (signal?: AbortSignal) => {
		try {
			setAuth(await getAuthStatus(signal))
			setAuthError(null)
		} catch (cause) {
			if (cause instanceof DOMException && cause.name === 'AbortError') return
			setAuthError(cause instanceof Error ? cause.message : '无法连接后端')
		}
	}, [])

	useEffect(() => {
		const controller = new AbortController()
		void refreshAuth(controller.signal)
		const unauthorized = () => setAuth((current) => ({ initialized: current?.initialized ?? true, authenticated: false }))
		window.addEventListener('homeloom:unauthorized', unauthorized)
		return () => { controller.abort(); window.removeEventListener('homeloom:unauthorized', unauthorized) }
	}, [refreshAuth])

	if (auth === null) return <main className="auth-shell"><section className="auth-card"><p className="eyebrow">HOMELOOM · ADMIN</p><h1>正在连接。</h1>{authError && <><p className="inline-error" role="alert">{authError}</p><button onClick={() => void refreshAuth()}>重试</button></>}</section></main>
	if (!auth.authenticated) return <AuthScreen initialized={auth.initialized} onSubmit={async (username, password) => setAuth(auth.initialized ? await login(username, password) : await setupAdministrator(username, password))} />
	return <Dashboard username={auth.username ?? 'admin'} onLogout={async () => { await logout(); setAuth({ initialized: true, authenticated: false }) }} />
}

function Dashboard({ username, onLogout }: { username: string, onLogout: () => Promise<void> }) {
  const [devices, setDevices] = useState<Device[]>([])
	const [targets, setTargets] = useState<Target[]>([])
	const [providers, setProviders] = useState<Provider[]>([])
	const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null)
	const [version, setVersion] = useState<SystemVersion | null>(null)
	const [commands, setCommands] = useState<DeviceCommand[]>([])
	const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
	const [runtimeSettings, setRuntimeSettings] = useState<RuntimeSettings | null>(null)
	const [modelCount, setModelCount] = useState(0)
	const commandHistoryLimit = useRef(1000)
	const [page, setPage] = usePageRoute()
	const [targetForm, setTargetForm] = useState<{ open: boolean, target: Target | null }>({ open: false, target: null })
	const [targetSection, setTargetSection] = useState<'devices' | 'homekit-camera' | 'matter-camera' | 'other-camera'>('devices')
	const [targetDeviceID, setTargetDeviceID] = useState<string | null>(null)
	const [providerForm, setProviderForm] = useState<{ open: boolean, provider: Provider | null }>({ open: false, provider: null })
	const [deviceProviderID, setDeviceProviderID] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set())
  const [live, setLive] = useState(false)
  const [deviceQuery, setDeviceQuery] = useState('')
  const [deviceStatus, setDeviceStatus] = useState<'all' | 'online' | 'offline' | 'unknown' | 'disabled' | 'removed'>('all')
  const [deviceProviderFilter, setDeviceProviderFilter] = useState('')
  const [deviceType, setDeviceType] = useState('')
  const [deviceHome, setDeviceHome] = useState('')
  const [deviceRoom, setDeviceRoom] = useState('')
  const [selectedDeviceID, setSelectedDeviceID] = useState<string | null>(null)
  const [mappingDevice, setMappingDevice] = useState<Device | null>(null)
  const [locationDeviceID, setLocationDeviceID] = useState<string | null>(null)
	const [locationHomes, setLocationHomes] = useState<DeviceLocationHome[]>([])
	const [locationCatalogOpen, setLocationCatalogOpen] = useState(false)
	const [logicalDeviceManagerOpen, setLogicalDeviceManagerOpen] = useState(false)
  const { toasts, notify, dismiss } = useToasts()

  const refresh = useCallback(async (signal?: AbortSignal) => {
    try {
	  const [deviceData, targetData, providerData, diagnosticData, commandData, auditData, versionData, settingsData, modelData, locationData] = await Promise.all([listDevices(signal), listTargets(signal), listProviders(signal), getDiagnostics(signal), listCommands(signal), listAuditEvents(signal).catch(() => []), getSystemVersion(signal).catch(() => null), getRuntimeSettings(signal).catch(() => null), listModelContracts(signal), listDeviceLocations(signal)])
	  setDevices(deviceData)
	  setTargets(targetData)
	  setProviders(providerData)
	  setDiagnostics(diagnosticData)
	  if (settingsData) commandHistoryLimit.current = settingsData.commandHistoryLimit
	  setCommands(commandData.slice(0, commandHistoryLimit.current))
	  setAuditEvents(auditData)
	  setVersion(versionData)
	  setRuntimeSettings(settingsData)
	  setModelCount(modelData.length)
	  setLocationHomes(locationData)
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
	  const reconcileTimer = window.setInterval(() => void refresh(), 5 * 60 * 1000)
		const unsubscribe = subscribeEvents({
			onConnection: setLive,
			onDevice: (updated) => setDevices((current) => { if (updated.removed) return current.filter((item) => item.id !== updated.id); const exists = current.some((item) => item.id === updated.id); return exists ? current.map((item) => item.id === updated.id ? updated : item) : [...current, updated] }),
			onCommand: (updated) => setCommands((current) => { const exists = current.some((item) => item.id === updated.id); const next = exists ? current.map((item) => item.id === updated.id ? updated : item) : [updated, ...current]; return next.sort((left, right) => new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime()).slice(0, commandHistoryLimit.current) }),
			onAudit: (updated) => setAuditEvents((current) => [updated, ...current.filter((item) => item.id !== updated.id)].slice(0, 200)),
			onTarget: (updated) => setTargets((current) => {
				if (updated.removed) return current.filter((item) => item.id !== updated.id)
				const exists = current.some((item) => item.id === updated.id)
				return exists ? current.map((item) => item.id === updated.id ? updated : item) : [...current, updated]
			}),
			onRuntime: (delta) => { if (delta.providers) setProviders(delta.providers); if (delta.diagnostics) setDiagnostics(delta.diagnostics) },
		})
	  return () => {
		controller.abort()
		window.clearInterval(reconcileTimer)
		unsubscribe()
	  }
  }, [refresh])

	useEffect(() => {
		const updateModelCount = (event: Event) => {
			const count = (event as CustomEvent<unknown>).detail
			if (typeof count === 'number' && Number.isInteger(count) && count >= 0) setModelCount(count)
		}
		window.addEventListener('homeloom:model-count', updateModelCount)
		return () => window.removeEventListener('homeloom:model-count', updateModelCount)
	}, [])

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
			notify('success', editing ? '目标配置已更新并实时应用' : '目标实例已创建')
		} catch (cause) {
			notify('error', cause instanceof Error ? cause.message : '保存目标失败')
			throw cause
		}
	}

	async function handleTargetDelete(target: Target) {
		if (!confirmTargetDeletion(target.name)) return
		try {
			await deleteTarget(target.id)
			setTargets((current) => current.filter((item) => item.id !== target.id))
			setTargetDeviceID((current) => current === target.id ? null : current)
			await refresh()
			notify('success', `目标“${target.name}”已删除`)
		} catch (cause) { notify('error', cause instanceof Error ? cause.message : '删除目标失败') }
	}
	async function handleTargetPairingRegenerate(target: Target) {
		const confirmation = confirmExactPhrase('这会更换新设备加入 Apple Home 时使用的 PIN 和 Setup ID；已有配对身份会保留。', `REGENERATE ${target.id}`)
		if (!confirmation) return
		try { await regenerateTargetPairing(target.id, confirmation); await refresh(); notify('success', `目标“${target.name}”的 HomeKit 配对参数已重新生成`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '重新生成 HomeKit 配对参数失败') }
	}
	async function handleTargetPairingIdentityClear(target: Target) {
		const confirmation = confirmExactPhrase('这会清除 HomeKit 密钥和所有控制器配对，必须在 Apple Home 中删除旧配件并重新添加。', `CLEAR ${target.id}`)
		if (!confirmation) return
		try { await clearTargetPairingIdentity(target.id, confirmation); await refresh(); notify('success', `目标“${target.name}”的 HomeKit 配对身份已清除并重建`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '清除 HomeKit 配对身份失败') }
	}
	async function handleMatterCommissioningToggle(target: MatterTarget, open: boolean) {
		let confirmation: string | null = null
		if (open) {
			confirmation = confirmExactPhrase('这会在局域网内临时公开 Matter 配网入口。只在准备添加可信控制器时执行。', `OPEN COMMISSIONING ${target.id}`)
			if (!confirmation) return
		}
		try {
			await (open ? openMatterCommissioningWindow(target.id, target.config.commissioningWindowSeconds, confirmation!) : closeMatterCommissioningWindow(target.id))
			await refresh()
			notify('success', open ? `目标“${target.name}”的 Matter 配网窗口已打开` : `目标“${target.name}”的 Matter 配网窗口已关闭`)
		} catch (cause) { notify('error', cause instanceof Error ? cause.message : '更新 Matter 配网窗口失败') }
	}
	async function handleMatterFabricDelete(target: MatterTarget, fabric: MatterFabric) {
		const label = fabric.label ?? fabric.id
		const confirmation = confirmExactPhrase(`这会立即撤销 Fabric“${label}”对桥内所有 Matter 设备的访问。`, `DELETE FABRIC ${target.id} ${fabric.id}`)
		if (!confirmation) return
		try { await deleteMatterFabric(target.id, fabric.id, confirmation); await refresh(); notify('success', `Matter Fabric“${label}”已删除`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '删除 Matter Fabric 失败') }
	}
	async function handleMatterFactoryReset(target: MatterTarget) {
		const confirmation = confirmExactPhrase('这会清除所有 Matter Fabric、运行时身份与配网资料；已加入的控制器将立即失去访问权限。', `FACTORY RESET ${target.id}`)
		if (!confirmation) return
		try { await factoryResetMatterTarget(target.id, confirmation); await refresh(); notify('success', `目标“${target.name}”已恢复 Matter 出厂身份`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '恢复 Matter 出厂身份失败') }
	}
	async function handleMatterCameraEnabled(target: MatterTarget, enabled: boolean) {
		try {
			await saveTarget({
				id: target.id, type: target.type, name: target.name, enabled,
				deviceIds: target.deviceIds, devices: target.devices,
				config: {
					networkInterface: target.config.networkInterface ?? '', udpPort: target.config.udpPort ?? null,
					discriminator: target.config.discriminator ?? null, passcode: null,
					vendorId: target.config.vendorId ?? null, productId: target.config.productId ?? null,
					productName: target.config.productName ?? '', serialNumber: target.config.serialNumber ?? '',
					commissioningWindowSeconds: target.config.commissioningWindowSeconds ?? null,
				},
			}, true)
			await refresh()
			notify('success', `目标“${target.name}”已${enabled ? '启用' : '停用'}`)
		} catch (cause) { notify('error', cause instanceof Error ? cause.message : '更新 Matter 摄像头状态失败') }
	}
	async function handleProviderSave(input: ProviderInput, editing: boolean) { try { await saveProvider(input, editing); setProviderForm({ open: false, provider: null }); await refresh(); notify('success', editing ? 'Provider 配置已更新' : 'Provider 已创建') } catch (cause) { notify('error', cause instanceof Error ? cause.message : '保存 Provider 失败'); throw cause } }
	async function handleProviderTest(input: ProviderInput) { try { await testProviderConnection(input); notify('success', 'Provider 连接测试成功') } catch (cause) { notify('error', cause instanceof Error ? cause.message : 'Provider 连接测试失败'); throw cause } }
	async function handleProviderDelete(provider: Provider) { if (!confirmProviderDeletion(provider.name)) return; try { await deleteProvider(provider.id); await refresh(); notify('success', `Provider“${provider.name}”已删除`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '删除 Provider 失败') } }
	async function handleProviderRestart(provider: Provider) { try { await restartProvider(provider.id); await refresh(); setError(null); notify('success', `Provider“${provider.name}”已重新启动`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : 'Provider 重新启动失败'); throw cause } }
	async function handleProviderCredentialRevocation(provider: Provider) {
		const confirmation = confirmExactPhrase('这会立即停止该 Provider、清除本地小米账号、Token、证书与会话。设备映射会保留，必须重新授权后才能启用。若管理员显式配置了受信任的 OAuth 注销端点，系统会尝试远端注销。', `REVOKE ${provider.id}`)
		if (!confirmation) return
		try {
			const result = await revokeProviderCredentials(provider.id, confirmation)
			await refresh()
			const warnings = [result.remoteError, result.disconnectError, result.reconciliationError].filter(Boolean)
			notify(warnings.length > 0 ? 'info' : 'success', warnings.length > 0 ? `Provider“${provider.name}”本地凭据已清除；${warnings.join('；')}` : `Provider“${provider.name}”的本地凭据已清除`)
		} catch (cause) { notify('error', cause instanceof Error ? cause.message : '注销 Provider 凭据失败'); throw cause }
	}
	async function handleProviderAuthChallengeComplete(updated: Provider) {
		setProviders((current) => current.map((item) => item.id === updated.id ? updated : item))
		await refresh()
		setError(null)
		notify('success', `Provider“${updated.name}”短信验证已完成`)
	}
	async function handleSimulation(device: Device, values: { availability?: DeviceAvailability; online?: boolean; power?: boolean; value?: number; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; active?: boolean; speed?: number; mode?: string; filterLife?: number; filterChange?: boolean; position?: number; sequence?: number; repeat?: number }) { try { const updated = await simulateDevice(device.id, values); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); setError(null); notify('info', `${device.name}模拟状态已更新`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '模拟状态失败'); throw cause } }
	async function handleDeviceEnabled(device: Device, enabled: boolean) { setPendingIds((current) => new Set(current).add(device.id)); try { const updated = await setDeviceEnabled(device.id, enabled); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); notify('success', `${device.name}已${enabled ? '启用' : '禁用'}`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '设备状态更新失败') } finally { setPendingIds((current) => { const next = new Set(current); next.delete(device.id); return next }) } }
	async function handleDeviceLocation(device: Device, value: DeviceLocationValue) { const updated = await setDeviceLocation(device.id, value); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); setLocationDeviceID(null); notify('success', `${device.name}的位置已更新`) }
	async function handlePropertyWrite(device: Device, endpointId: string, capabilityId: string, propertyId: string, value: PropertyValue) { try { const updated = await setDeviceProperty(device.id, endpointId, capabilityId, propertyId, value); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); const [diagnosticData, commandData] = await Promise.all([getDiagnostics(), listCommands()]); setDiagnostics(diagnosticData); setCommands(commandData); setError(null); notify('success', `${device.name}.${propertyId} 写入成功`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '属性写入失败'); throw cause } }
	async function handleCommandExecute(device: Device, endpointId: string, capabilityId: string, commandId: string, parameters: Record<string, PropertyValue>, idempotencyKey: string) { try { const updated = await executeDeviceCommand(device.id, endpointId, capabilityId, commandId, parameters, idempotencyKey); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); const [diagnosticData, commandData] = await Promise.all([getDiagnostics(), listCommands()]); setDiagnostics(diagnosticData); setCommands(commandData); setError(null); notify('success', `${device.name}.${commandId} 执行成功`) } catch (cause) { notify('error', cause instanceof Error ? cause.message : '命令执行失败'); throw cause } }
	async function handleRuntimeSettingsSave(next: RuntimeSettings) { try { const saved = await saveRuntimeSettings(next); commandHistoryLimit.current = saved.commandHistoryLimit; setCommands((current) => current.slice(0, saved.commandHistoryLimit)); setRuntimeSettings(saved); notify('success', '运行时设置已保存并实时生效') } catch (cause) { notify('error', cause instanceof Error ? cause.message : '保存运行时设置失败'); throw cause } }
	const deviceProvider = deviceProviderID ? providers.find((item) => item.id === deviceProviderID && supportsProviderChildDevices(item.type)) ?? null : null
	const locationDevice = locationDeviceID ? devices.find((item) => item.id === locationDeviceID) ?? null : null
	const pageCopy = page === 'devices' ? { title: '把家的状态织在一起。', intro: '设备状态驻留内存，映射从每台设备独立进入配置。', eyebrow: 'DEVICES', section: '设备中心' } : page === 'providers' ? { title: '让所有数据源有序接入。', intro: 'Virtual、MQTT、小米中枢与第三方兼容 MIoT 云按独立实例运行。', eyebrow: 'PROVIDERS', section: '设备来源管理' } : page === 'targets' ? { title: '一个目标，或很多个目标。', intro: '每个目标实例选择独立的 Consumer 适配器、设备身份和属性目录；协议专属配置互不混用。', eyebrow: 'TARGETS', section: '桥接中心' } : page === 'mapping' ? { title: '定义一次，处处使用。', intro: '集中查看统一设备模型，配置端点、能力和属性三级字段；设备路由仍从对应设备进入。', eyebrow: 'UNIFIED MODELS', section: '统一模型配置' } : page === 'ai' ? { title: '把能力交给 AI，把决定留给你。', intro: '集中设置 AI 服务，并明确每台设备和每个属性的可见范围与操作边界。', eyebrow: 'AI CONTROL', section: 'AI 服务与设备授权' } : { title: '看见系统的每一次呼吸。', intro: '观察事件队列、设备连接和命令生命周期。', eyebrow: 'SYSTEM', section: '系统诊断' }
	const summary = page === 'devices' ? devices.filter((item) => item.availability === 'online').length : page === 'providers' ? providers.filter((item) => item.status === 'running').length : page === 'targets' ? targets.filter((item) => item.status === 'running').length : page === 'mapping' ? modelCount : page === 'ai' ? devices.filter((item) => !item.removed).length : diagnostics?.eventsProcessed ?? 0
	const deviceHomeOptions = useMemo(() => homeLocationOptions(devices), [devices])
	const deviceRoomOptions = useMemo(() => roomLocationOptions(devices, deviceHome), [devices, deviceHome])
	const deviceTypes = useMemo(() => Array.from(new Set(devices.map((item) => item.type))).sort((left, right) => deviceTypeLabel(left).localeCompare(deviceTypeLabel(right), 'zh-CN')), [devices])
	const filteredDevices = devices.filter((item) => {
		const query = deviceQuery.trim().toLowerCase()
		const matchesText = `${item.name} ${item.id} ${item.providerId} ${item.homeName ?? ''} ${item.roomName ?? ''}`.toLowerCase().includes(query)
		const matchesStatus = deviceStatus === 'all' || (deviceStatus === 'disabled' ? item.disabled : deviceStatus === 'removed' ? item.removed : item.availability === deviceStatus && !item.disabled && !item.removed)
		return matchesText && matchesStatus && (!deviceProviderFilter || item.providerId === deviceProviderFilter) && (!deviceType || item.type === deviceType) && matchesDeviceLocation(item, deviceHome, deviceRoom)
	})
	const selectedDevice = selectedDeviceID ? devices.find((item) => item.id === selectedDeviceID) ?? null : null
	const targetDeviceTarget = targetDeviceID ? targets.find((item) => item.id === targetDeviceID) ?? null : null
	const targetCreateType: TargetType = targetSection === 'homekit-camera' ? 'homekit-camera' : targetSection === 'matter-camera' ? 'matter-camera' : 'apple-hap'
	const visibleTargets = targets.filter((item) => targetSection === 'devices'
		? item.type === 'apple-hap' || item.type === 'matter'
		: targetSection === 'homekit-camera' ? item.type === 'homekit-camera' : targetSection === 'matter-camera' ? item.type === 'matter-camera' : false)

  return (<>
	<a className="skip-link" href="#main-content">跳到主要内容</a>
    <main id="main-content" tabIndex={-1}>
	  <nav className="top-nav" aria-label="主要导航">
	    <a className="wordmark" href="#/devices" aria-label="HomeLoom 设备中心"><BrandMark /></a>
	    <div className="nav-links">
	      <button aria-current={page === 'devices' ? 'page' : undefined} className={page === 'devices' ? 'is-active' : ''} onClick={() => setPage('devices')}>设备</button>
	      <button aria-current={page === 'providers' ? 'page' : undefined} className={page === 'providers' ? 'is-active' : ''} onClick={() => setPage('providers')}>设备来源</button>
	      <button aria-current={page === 'targets' ? 'page' : undefined} className={page === 'targets' ? 'is-active' : ''} onClick={() => setPage('targets')}>桥接中心</button>
	      <button aria-current={page === 'mapping' ? 'page' : undefined} className={page === 'mapping' ? 'is-active' : ''} onClick={() => setPage('mapping')}>统一模型</button>
	      <button aria-current={page === 'ai' ? 'page' : undefined} className={page === 'ai' ? 'is-active' : ''} onClick={() => setPage('ai')}>AI</button>
	      <button aria-current={page === 'system' ? 'page' : undefined} className={page === 'system' ? 'is-active' : ''} onClick={() => setPage('system')}>系统</button>
	    </div>
	    <span className="runtime-meta"><span className="version-badge" title={version ? `${version.commit} · ${version.buildTime}` : '版本读取中'}>{version?.version ?? '…'}</span><span aria-live="polite" className={`live-indicator ${live ? 'is-live' : ''}`}>{live ? '实时' : '重连中'}</span><button className="logout-button" title={`当前管理员：${username}`} onClick={() => void onLogout().catch((cause) => notify('error', cause instanceof Error ? cause.message : '退出失败'))}>退出</button></span>
	  </nav>
      <header className="hero">
        <div>
          <p className="eyebrow">HOMELOOM · DEMO 01</p>
		  <h1>{pageCopy.title}</h1><p className="intro">{pageCopy.intro}</p>
        </div>
		<div className="summary" aria-label={`${summary} ${page === 'devices' ? '在线设备' : page === 'providers' ? '运行中设备来源' : page === 'targets' ? '运行中目标' : page === 'mapping' ? '统一设备模型' : page === 'ai' ? '可授权设备' : '已处理事件'}`}>
		  <span>{summary}</span><small>{page === 'devices' ? '在线设备' : page === 'providers' ? '运行中设备来源' : page === 'targets' ? '运行中目标' : page === 'mapping' ? '统一设备模型' : page === 'ai' ? '可授权设备' : '已处理事件'}</small>
        </div>
      </header>

      <section className="section-heading">
        <div>
		  <p className="eyebrow">{pageCopy.eyebrow}</p><h2>{pageCopy.section}</h2>
        </div>
		<div className="heading-actions">{page === 'devices' && <button className="add-button" onClick={() => setLogicalDeviceManagerOpen(true)}>设备链接</button>}{page === 'providers' && <button className="add-button" onClick={() => setLocationCatalogOpen(true)}>管理家庭与房间</button>}{page === 'providers' && !deviceProvider && <button className="add-button" onClick={() => setProviderForm({ open: true, provider: null })}>＋ 新建设备来源</button>}{page === 'targets' && (targetSection === 'devices' || targetSection === 'homekit-camera' || targetSection === 'matter-camera') && <button className="add-button" onClick={() => setTargetForm({ open: true, target: null })}>＋ {targetSection === 'homekit-camera' || targetSection === 'matter-camera' ? '发布摄像头' : '新建目标'}</button>}{page !== 'devices' && <button className="refresh-button" onClick={() => void refresh()} disabled={loading}>刷新状态</button>}</div>
      </section>
	  {page === 'devices' && <div className="device-filters" aria-label="设备筛选">
		<label className="device-search"><i aria-hidden="true" /><input aria-label="搜索设备" value={deviceQuery} onChange={(event) => setDeviceQuery(event.target.value)} placeholder="搜索名称、ID、来源或位置" /></label>
		<select aria-label="设备状态" value={deviceStatus} onChange={(event) => setDeviceStatus(event.target.value as typeof deviceStatus)}><option value="all">全部状态</option><option value="online">仅在线</option><option value="offline">暂时离线</option><option value="unknown">可用性未知</option><option value="disabled">人工禁用</option><option value="removed">来源已删除</option></select>
		<select aria-label="设备来源筛选" value={deviceProviderFilter} onChange={(event) => setDeviceProviderFilter(event.target.value)}><option value="">全部设备来源</option>{providers.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.id}</option>)}</select>
		<select aria-label="统一模型筛选" value={deviceType} onChange={(event) => setDeviceType(event.target.value)}><option value="">全部统一模型</option>{deviceTypes.map((item) => <option key={item} value={item}>{deviceTypeLabel(item)}（{item}）</option>)}</select>
		<select aria-label="家庭筛选" value={deviceHome} onChange={(event) => { setDeviceHome(event.target.value); setDeviceRoom('') }}><option value="">全部家庭</option>{deviceHomeOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>
		<select aria-label="房间筛选" value={deviceRoom} onChange={(event) => setDeviceRoom(event.target.value)}><option value="">全部房间</option>{deviceRoomOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>
		<span className="device-count" role="status">{filteredDevices.length} / {devices.length}</span><button className="refresh-button" onClick={() => void refresh()} disabled={loading}>刷新</button>
	  </div>}

      {error && <div className="error-banner" role="alert">{error}，请确认后端已在 8090 端口运行。</div>}
      {loading ? (
        <LoadingState />
      ) : (
		<Suspense fallback={<LoadingState />}>
		{page === 'devices' ? <section className="device-grid" aria-label="设备列表">
		  {filteredDevices.map((device) => (
            <DeviceCard
              key={device.id}
              device={device}
              pending={pendingIds.has(device.id)}
              onPowerChange={(item, value) => void handlePowerChange(item, value)}
			  onDetails={(item) => setSelectedDeviceID(item.id)}
			  onMapping={setMappingDevice}
			  onEnabledChange={(item, enabled) => void handleDeviceEnabled(item, enabled)}
            />
          ))}
		  {filteredDevices.length === 0 && <CollectionEmpty title="没有匹配的设备" description={devices.length ? '请调整搜索文字或在线状态筛选。' : '启用 Provider 后，发现的设备会显示在这里。'} />}
		</section> : page === 'providers' ? deviceProvider ? deviceProvider.type === 'virtual' ? <VirtualDeviceManager provider={deviceProvider} devices={devices.filter((item) => item.providerId === deviceProvider.id && !item.removed)} onClose={() => setDeviceProviderID(null)} onSave={async (input, editing) => { await handleProviderSave(input, editing); setDeviceProviderID(null) }} /> : deviceProvider.type === 'mqtt' ? <MQTTDeviceManager provider={deviceProvider} devices={devices.filter((item) => item.providerId === deviceProvider.id && !item.removed)} onClose={() => setDeviceProviderID(null)} onSave={async (input, editing) => { await handleProviderSave(input, editing); setDeviceProviderID(null) }} /> : deviceProvider.type === 'camera' ? <CameraDeviceManager provider={deviceProvider} providers={providers} devices={devices} onClose={() => setDeviceProviderID(null)} onSave={async (input, editing) => { await handleProviderSave(input, editing); setDeviceProviderID(null) }} /> : deviceProvider.type === 'gree' ? <GreeDeviceManager provider={deviceProvider} devices={devices.filter((item) => item.providerId === deviceProvider.id && !item.removed)} onClose={() => setDeviceProviderID(null)} onSave={async (input, editing) => { await handleProviderSave(input, editing); setDeviceProviderID(null) }} /> : <XiaomiDeviceManager provider={deviceProvider} devices={devices.filter((item) => item.providerId === deviceProvider.id && !item.removed)} locations={locationHomes} onManageLocations={() => setLocationCatalogOpen(true)} onMapping={setMappingDevice} onClose={() => setDeviceProviderID(null)} onSave={handleProviderSave} /> : <ProviderWorkspace providers={providers} devices={devices} onEdit={(item) => setProviderForm({ open: true, provider: item })} onManageDevices={(item) => setDeviceProviderID(item.id)} onDeviceLocation={(item) => setLocationDeviceID(item.id)} onDelete={(item) => void handleProviderDelete(item)} onRestart={handleProviderRestart} onRevokeCredentials={handleProviderCredentialRevocation} onTest={handleProviderTest} onAuthChallengeComplete={handleProviderAuthChallengeComplete} onSimulate={handleSimulation} /> : page === 'mapping' ? <MappingWorkspace /> : page === 'ai' ? <AIWorkspace devices={devices.filter((item) => !item.removed)} /> : page === 'system' ? <SystemDashboard diagnostics={diagnostics} commands={commands} auditEvents={auditEvents} settings={runtimeSettings} onSettingsSave={handleRuntimeSettingsSave} /> : targetDeviceTarget ? <TargetDeviceManager target={targetDeviceTarget} devices={devices.filter((item) => !item.removed)} onClose={() => setTargetDeviceID(null)} onSave={async (input) => { await handleTargetSave(input, true) }} onConfirmMatterEndpointType={async (consumerDeviceID, deviceType, confirmation) => { await confirmMatterEndpointDeviceType(targetDeviceTarget.id, consumerDeviceID, deviceType, confirmation); await refresh(); notify('success', `Matter Endpoint“${consumerDeviceID}”已切换为 ${deviceType}`) }} /> : <section className="target-list">
		  <nav className="target-subnav" aria-label="目标类型分页"><button className={targetSection === 'devices' ? 'is-active' : ''} onClick={() => setTargetSection('devices')}>普通设备</button><button className={targetSection === 'homekit-camera' ? 'is-active' : ''} onClick={() => setTargetSection('homekit-camera')}>HomeKit 摄像头</button><button className={targetSection === 'matter-camera' ? 'is-active' : ''} onClick={() => setTargetSection('matter-camera')}>Matter 摄像头</button><button className={targetSection === 'other-camera' ? 'is-active' : ''} onClick={() => setTargetSection('other-camera')}>其他摄像头</button></nav>
		  <div className="config-note">
		    <span>配置来源</span>
		    <strong>{targetSection === 'homekit-camera' ? '独立 HAP Camera Target' : targetSection === 'matter-camera' ? 'Matter 1.5+ Camera' : targetSection === 'other-camera' ? '其他媒体消费协议' : 'PostgreSQL · targets'}</strong>
		    <p>{targetSection === 'homekit-camera' ? '每个目标只发布一台摄像头，拥有独立 HAP/mDNS 与配对身份，不进入普通 Apple Home Bridge。' : targetSection === 'matter-camera' ? 'Matter Camera 使用 Matter 1.5+ Camera 与 WebRTC，每个 Target 只选择一台摄像头。此能力为实验性 Controller 兼容，不保证 Apple Home 支持；配网使用 Matter QR，不使用 HomeKit PIN。' : targetSection === 'other-camera' ? '预留 RTSP restream、NVR、ONVIF Profile S、厂商云等消费目标；当前没有已注册适配器。' : '普通 HomeKit Bridge 与 Matter Bridge 管理非摄像头设备；Camera 必须从专属分页发布。'}</p>
		  </div>
		  {visibleTargets.map((target) => <TargetCard key={target.id} target={target} sourceDevice={target.type === 'homekit-camera' || target.type === 'matter-camera' ? devices.find((item) => item.id === (target.devices[0]?.sourceDeviceId ?? target.deviceIds[0])) : undefined} onPreviewCamera={(item) => setSelectedDeviceID(item.id)} onEdit={(item) => setTargetForm({ open: true, target: item })} onManageDevices={(item) => setTargetDeviceID(item.id)} onDelete={(item) => void handleTargetDelete(item)} onRegeneratePairing={(item) => void handleTargetPairingRegenerate(item)} onClearPairingIdentity={(item) => void handleTargetPairingIdentityClear(item)} onMatterCommissioningToggle={(item, open) => void handleMatterCommissioningToggle(item, open)} onDeleteMatterFabric={(item, fabric) => void handleMatterFabricDelete(item, fabric)} onFactoryResetMatter={(item) => void handleMatterFactoryReset(item)} onEnabledChange={(item, enabled) => void handleMatterCameraEnabled(item, enabled)} />)}
		  {visibleTargets.length === 0 && <CollectionEmpty title={targetSection === 'homekit-camera' || targetSection === 'matter-camera' ? '还没有发布摄像头' : targetSection === 'devices' ? '还没有普通设备目标' : '当前适配器尚未开放'} description={targetSection === 'homekit-camera' ? '点击“发布摄像头”，选择设备中心中的 Camera 并创建独立 HomeKit Camera Target。' : targetSection === 'matter-camera' ? '点击“发布摄像头”，选择设备中心中的 Camera 并创建实验性 Matter Camera Target。' : targetSection === 'devices' ? '新建普通 HomeKit 或 Matter 目标后，再配置消费端设备。' : '该分页用于明确协议边界，待运行时能力完成后再开放创建。'} />}
		</section>}
		</Suspense>
      )}
	  {targetForm.open && <TargetForm target={targetForm.target} devices={devices} initialType={targetCreateType} onCancel={() => setTargetForm({ open: false, target: null })} onSave={handleTargetSave} />}
	  {providerForm.open && <Suspense fallback={null}><ProviderForm provider={providerForm.provider} onCancel={() => setProviderForm({ open: false, provider: null })} onSave={handleProviderSave} onTest={handleProviderTest} /></Suspense>}
	  {selectedDevice && <Suspense fallback={null}><DeviceDetails device={selectedDevice} onClose={() => setSelectedDeviceID(null)} onPropertyWrite={(endpointId, capabilityId, propertyId, value) => handlePropertyWrite(selectedDevice, endpointId, capabilityId, propertyId, value)} onCommandExecute={(endpointId, capabilityId, commandId, parameters, idempotencyKey) => handleCommandExecute(selectedDevice, endpointId, capabilityId, commandId, parameters, idempotencyKey)} /></Suspense>}
	  {mappingDevice && <Suspense fallback={null}><DeviceMappingDialog device={mappingDevice} onClose={() => setMappingDevice(null)} /></Suspense>}
	  {locationDevice && <DeviceLocationDialog key={locationDevice.id} device={locationDevice} homes={locationHomes} onManage={() => setLocationCatalogOpen(true)} onCancel={() => setLocationDeviceID(null)} onSave={(value) => handleDeviceLocation(locationDevice, value)} />}
	  {locationCatalogOpen && <DeviceLocationCatalogDialog homes={locationHomes} onChange={setLocationHomes} onClose={() => setLocationCatalogOpen(false)} />}
	  {logicalDeviceManagerOpen && <Suspense fallback={null}><LogicalDeviceManager devices={devices} onClose={() => setLogicalDeviceManagerOpen(false)} onChanged={refresh} /></Suspense>}
	  <ToastCenter toasts={toasts} dismiss={dismiss} />
    </main>
	</>)
}
