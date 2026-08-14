import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'
import type { MatterTarget, Target, TargetInput } from './types/target'

const api = vi.hoisted(() => ({
  getAuthStatus: vi.fn(), login: vi.fn(), logout: vi.fn(), setupAdministrator: vi.fn(),
  listDevices: vi.fn(), listDeviceLocations: vi.fn(), createDeviceLocationHome: vi.fn(), updateDeviceLocationHome: vi.fn(), deleteDeviceLocationHome: vi.fn(), createDeviceLocationRoom: vi.fn(), updateDeviceLocationRoom: vi.fn(), deleteDeviceLocationRoom: vi.fn(), setDeviceEnabled: vi.fn(), setDeviceLocation: vi.fn(), setDevicePower: vi.fn(), setDeviceProperty: vi.fn(), simulateDevice: vi.fn(), executeDeviceCommand: vi.fn(),
  listTargets: vi.fn(), saveTarget: vi.fn(), deleteTarget: vi.fn(), regenerateTargetPairing: vi.fn(), clearTargetPairingIdentity: vi.fn(), openMatterCommissioningWindow: vi.fn(), closeMatterCommissioningWindow: vi.fn(), deleteMatterFabric: vi.fn(), factoryResetMatterTarget: vi.fn(), confirmMatterEndpointDeviceType: vi.fn(),
  listProviders: vi.fn(), saveProvider: vi.fn(), deleteProvider: vi.fn(), restartProvider: vi.fn(), testProviderConnection: vi.fn(),
  getDiagnostics: vi.fn(), getRuntimeSettings: vi.fn(), listAuditEvents: vi.fn(), listCommands: vi.fn(), saveRuntimeSettings: vi.fn(),
  subscribeEvents: vi.fn(),
  getSystemVersion: vi.fn(),
  listModelContracts: vi.fn(), listCustomModelProperties: vi.fn(),
}))

const runtimeDiagnostics = { eventsReceived: 0, eventsProcessed: 0, eventsDropped: 0, eventQueuePending: 0, eventQueueCapacity: 1, targetEventsDropped: 0, stateEventsDropped: 0, statesMarkedStale: 0, commandsStarted: 0, commandsConfirmed: 0, commandsRejected: 0, commandsTimedOut: 0, commandsSuperseded: 0, commandsOutcomeUnknown: 0, homeKitPushes: 0, onlineDevices: 0, offlineDevices: 0, unknownDevices: 0, providersRunning: 0, providerRetries: 0, deviceSubscribers: 0, stateSubscribers: 0, commandAverageLatencyMs: 0, commandQueuePending: 0, commandQueueMaxPending: 0, eventAverageLatencyMs: 0, eventMaxLatencyMs: 0, slowEventHandlers: 0, databaseOperations: 0, databaseAverageLatencyMs: 0, databaseMaxLatencyMs: 0, providerClockSkewEvents: 0, providerMaxClockSkewMs: 0, goroutines: 1, heapAllocBytes: 0, heapObjects: 0 }

vi.mock('./api/auth', () => ({ getAuthStatus: api.getAuthStatus, login: api.login, logout: api.logout, setupAdministrator: api.setupAdministrator }))
vi.mock('./api/devices', () => ({
  listDevices: api.listDevices, listDeviceLocations: api.listDeviceLocations, createDeviceLocationHome: api.createDeviceLocationHome, updateDeviceLocationHome: api.updateDeviceLocationHome, deleteDeviceLocationHome: api.deleteDeviceLocationHome, createDeviceLocationRoom: api.createDeviceLocationRoom, updateDeviceLocationRoom: api.updateDeviceLocationRoom, deleteDeviceLocationRoom: api.deleteDeviceLocationRoom, setDeviceEnabled: api.setDeviceEnabled, setDeviceLocation: api.setDeviceLocation, setDevicePower: api.setDevicePower, setDeviceProperty: api.setDeviceProperty, simulateDevice: api.simulateDevice, executeDeviceCommand: api.executeDeviceCommand,
  subscribeDevices: (_handler: unknown, onStatus: (live: boolean) => void) => { onStatus(true); return () => {} },
}))
vi.mock('./api/targets', () => ({
  listTargets: api.listTargets, saveTarget: api.saveTarget, deleteTarget: api.deleteTarget, regenerateTargetPairing: api.regenerateTargetPairing, clearTargetPairingIdentity: api.clearTargetPairingIdentity, openMatterCommissioningWindow: api.openMatterCommissioningWindow, closeMatterCommissioningWindow: api.closeMatterCommissioningWindow, deleteMatterFabric: api.deleteMatterFabric, factoryResetMatterTarget: api.factoryResetMatterTarget, confirmMatterEndpointDeviceType: api.confirmMatterEndpointDeviceType,
  subscribeTargets: () => () => {},
}))
vi.mock('./api/providers', () => ({ listProviders: api.listProviders, saveProvider: api.saveProvider, deleteProvider: api.deleteProvider, restartProvider: api.restartProvider, testProviderConnection: api.testProviderConnection }))
vi.mock('./api/diagnostics', () => ({
  getDiagnostics: api.getDiagnostics, getRuntimeSettings: api.getRuntimeSettings, listAuditEvents: api.listAuditEvents, listCommands: api.listCommands, saveRuntimeSettings: api.saveRuntimeSettings,
  subscribeAuditEvents: () => () => {}, subscribeCommands: () => () => {},
}))
vi.mock('./api/events', () => ({ subscribeEvents: api.subscribeEvents }))
vi.mock('./api/system', () => ({ getSystemVersion: api.getSystemVersion }))
vi.mock('./api/mapping', async (importOriginal) => {
  const original = await importOriginal<typeof import('./api/mapping')>()
  return { ...original, listModelContracts: api.listModelContracts, listCustomModelProperties: api.listCustomModelProperties }
})

beforeEach(() => {
  vi.clearAllMocks()
  window.history.replaceState(null, '', '#/devices')
  api.listDevices.mockResolvedValue([])
	api.listDeviceLocations.mockResolvedValue([])
  api.listTargets.mockResolvedValue([])
  api.listProviders.mockResolvedValue([])
  api.getDiagnostics.mockResolvedValue({ eventsProcessed: 0 })
  api.listCommands.mockResolvedValue([])
  api.listAuditEvents.mockResolvedValue([])
  api.getSystemVersion.mockResolvedValue({ version: 'test', commit: 'abc', buildTime: 'now' })
  api.getRuntimeSettings.mockResolvedValue(null)
  api.listModelContracts.mockResolvedValue(Array.from({ length: 36 }, (_, index) => ({ deviceType: `model-${index + 1}`, name: `模型 ${index + 1}`, version: 1, builtIn: true, parameters: [], custom: { publisher: { level: 'custom', behavior: 'preserve-and-mark-custom' }, consumer: { level: 'custom', behavior: 'explicit-path-mapping-only' } } })))
  api.listCustomModelProperties.mockResolvedValue([])
  api.subscribeEvents.mockReturnValue(() => {})
  api.logout.mockResolvedValue(undefined)
	api.openMatterCommissioningWindow.mockResolvedValue(undefined)
	api.closeMatterCommissioningWindow.mockResolvedValue(undefined)
	api.deleteMatterFabric.mockResolvedValue(undefined)
	api.factoryResetMatterTarget.mockResolvedValue(undefined)
	api.confirmMatterEndpointDeviceType.mockResolvedValue(undefined)
})

describe('App integration', () => {
	it('uses one unified SSE connection and applies runtime deltas', async () => {
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		const intervalSpy = vi.spyOn(window, 'setInterval')
		render(<App />)
		await screen.findByRole('button', { name: '退出' })
		await waitFor(() => expect(api.subscribeEvents).toHaveBeenCalledOnce())
		expect(intervalSpy).toHaveBeenCalledWith(expect.any(Function), 5 * 60 * 1000)
		expect(intervalSpy).not.toHaveBeenCalledWith(expect.any(Function), 30 * 1000)
		const handlers = api.subscribeEvents.mock.calls[0][0] as { onRuntime: (snapshot: { providers?: unknown[]; diagnostics?: typeof runtimeDiagnostics }) => void }
		act(() => handlers.onRuntime({ providers: [], diagnostics: { ...runtimeDiagnostics, eventsProcessed: 42 } }))
		await userEvent.click(screen.getByRole('button', { name: '系统' }))
		expect(await screen.findByLabelText('42 已处理事件')).toBeInTheDocument()
		expect(api.listDevices).toHaveBeenCalledOnce()
		intervalSpy.mockRestore()
	})

	it('removes a device immediately when the Provider publishes a removal tombstone', async () => {
		const item = { schemaVersion: 1, id: 'living-light', providerId: 'xiaomi-main', name: '客厅灯', type: 'lightbulb', availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-07-22T00:00:00Z' }
		api.listDevices.mockResolvedValue([item])
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		render(<App />)
		expect(await screen.findByRole('heading', { name: '客厅灯' })).toBeInTheDocument()
		const handlers = api.subscribeEvents.mock.calls[0][0] as { onDevice: (device: typeof item & { removed?: boolean }) => void }

		act(() => handlers.onDevice({ ...item, availability: 'offline', online: false, removed: true }))

		await waitFor(() => expect(screen.queryByRole('heading', { name: '客厅灯' })).not.toBeInTheDocument())
		expect(screen.getByRole('status')).toHaveTextContent('0 / 0')
	})

	it('removes a Matter bridge when the target stream publishes its deletion tombstone', async () => {
		api.listTargets.mockResolvedValue([{
			id: 'matter-main', type: 'matter', name: 'Matter 主桥', enabled: true, status: 'running',
			deviceIds: [], devices: [], config: {}, commissioning: { state: 'uncommissioned', windowOpen: false },
			fabricCount: 0, endpointCount: 0,
		}])
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		render(<App />)
		const user = userEvent.setup()
		await user.click(await screen.findByRole('button', { name: '桥接中心' }))
		expect(await screen.findByRole('heading', { name: 'Matter 主桥' })).toBeInTheDocument()
		const handlers = api.subscribeEvents.mock.calls[0][0] as { onTarget: (target: MatterTarget) => void }

		act(() => handlers.onTarget({
			id: 'matter-main', type: 'matter', name: 'Matter 主桥', enabled: false, status: 'error',
			deviceIds: [], devices: [], config: {}, commissioning: { state: 'unknown', windowOpen: false },
			fabricCount: 0, endpointCount: 0, removed: true,
		}))

		await waitFor(() => expect(screen.queryByRole('heading', { name: 'Matter 主桥' })).not.toBeInTheDocument())
		expect(screen.getByText('还没有普通设备目标')).toBeInTheDocument()
	})

  it('initializes the sole administrator and loads the dashboard', async () => {
    api.getAuthStatus.mockResolvedValue({ initialized: false, authenticated: false })
    api.setupAdministrator.mockResolvedValue({ initialized: true, authenticated: true, username: 'owner' })
    render(<App />)

    const user = userEvent.setup()
    await user.clear(await screen.findByLabelText('用户名'))
    await user.type(screen.getByLabelText('用户名'), 'owner')
    await user.type(screen.getByLabelText('密码'), 'a-long-password')
    await user.type(screen.getByLabelText('确认密码'), 'a-long-password')
    await user.click(screen.getByRole('button', { name: '创建管理员' }))

    expect(await screen.findByRole('button', { name: '退出' })).toHaveAttribute('title', '当前管理员：owner')
    expect(api.setupAdministrator).toHaveBeenCalledWith('owner', 'a-long-password')
    expect(await screen.findByText('没有匹配的设备')).toBeInTheDocument()
  })

  it('navigates management pages and returns to login after logout', async () => {
    api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
    render(<App />)
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: '桥接中心' }))
    expect(await screen.findByRole('heading', { name: '桥接中心' })).toBeInTheDocument()
    expect(screen.getByText('还没有普通设备目标')).toBeInTheDocument()
	await user.click(screen.getByRole('button', { name: '设备来源' }))
	expect(await screen.findByRole('heading', { name: '设备来源管理' })).toBeInTheDocument()
	expect(screen.getByRole('button', { name: '＋ 新建设备来源' })).toBeInTheDocument()
    expect(await screen.findByText('还没有 Provider')).toBeInTheDocument()
	expect(screen.queryByRole('button', { name: '米家' })).not.toBeInTheDocument()
	expect(await screen.findByText('一种生命周期，接入所有数据源。')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '退出' }))
    await waitFor(() => expect(api.logout).toHaveBeenCalledOnce())
    expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  })

  it('reads the unified model summary count from the model catalog', async () => {
    api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
    render(<App />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '统一模型' }))
    expect(await screen.findByLabelText('36 统一设备模型')).toBeInTheDocument()
    expect(api.listModelContracts).toHaveBeenCalled()
  })

	it('separates HomeKit Camera publication from ordinary bridge targets', async () => {
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		render(<App />)
		const user = userEvent.setup()
		await user.click(await screen.findByRole('button', { name: '桥接中心' }))
		await user.click(screen.getByRole('button', { name: 'HomeKit 摄像头' }))
		expect(screen.getByText('独立 HAP Camera Target')).toBeInTheDocument()
		expect(screen.getByText('还没有发布摄像头')).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '＋ 发布摄像头' })).toBeInTheDocument()
		await user.click(screen.getByRole('button', { name: 'Matter 摄像头' }))
		expect(screen.getByText('Matter 1.5+ Camera')).toBeInTheDocument()
		expect(screen.getByText(/实验性 Controller 兼容，不保证 Apple Home 支持/)).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '＋ 发布摄像头' })).toBeInTheDocument()
		await user.click(screen.getByRole('button', { name: '＋ 发布摄像头' }))
		expect(screen.getByRole('combobox', { name: /目标类型（type）/ })).toHaveValue('matter-camera')
		expect(screen.queryByLabelText(/HomeKit 8 位配对码/)).not.toBeInTheDocument()
		expect(screen.getByLabelText(/配网辨识码/)).toBeInTheDocument()
	})

	it('does not restore a deleted Consumer device when binding a replacement', async () => {
		let bridge: Target = {
			id: 'apple-main', type: 'apple-hap', name: 'HomeKit 主桥', enabled: true, status: 'running',
			config: { address: ':51826', setupId: 'HLM1' }, pairing: { paired: false }, deviceIds: ['source-switch'],
			devices: [{ id: 'old-switch', name: '旧开关', type: 'switch', sourceDeviceId: 'source-switch', enabled: true }],
		}
		api.listDevices.mockResolvedValue([{ schemaVersion: 1, id: 'source-switch', providerId: 'virtual-main', name: '来源开关', type: 'switch', availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-08-13T00:00:00Z' }])
		api.listTargets.mockImplementation(async () => [bridge])
		api.saveTarget.mockImplementation(async (input: TargetInput) => {
			bridge = { ...bridge, deviceIds: input.deviceIds, devices: input.devices }
			return bridge
		})
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		render(<App />)
		const user = userEvent.setup()

		await user.click(await screen.findByRole('button', { name: '桥接中心' }))
		await user.click(await screen.findByRole('button', { name: '配置消费端设备' }))
		expect(await screen.findByDisplayValue('旧开关')).toBeInTheDocument()
		await user.click(screen.getByRole('button', { name: '删除' }))
		await user.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		await waitFor(() => expect(api.saveTarget).toHaveBeenCalledTimes(1))
		await waitFor(() => expect(api.listTargets).toHaveBeenCalledTimes(2))

		await user.click(screen.getByRole('button', { name: '返回桥接中心' }))
		await user.click(await screen.findByRole('button', { name: '配置消费端设备' }))
		expect(screen.queryByDisplayValue('旧开关')).not.toBeInTheDocument()
		await user.click(screen.getByRole('button', { name: '＋ 添加消费端设备' }))
		await user.click(screen.getByRole('button', { name: '保存消费端设备并应用目标' }))
		await waitFor(() => expect(api.saveTarget).toHaveBeenCalledTimes(2))
		expect((api.saveTarget.mock.calls[1][0] as TargetInput).devices).toEqual([
			expect.objectContaining({ id: 'apple-main-source-switch', sourceDeviceId: 'source-switch' }),
		])
	})

  it('returns an authenticated dashboard to login on a global unauthorized event', async () => {
    api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
    render(<App />)
    await screen.findByRole('button', { name: '退出' })
    window.dispatchEvent(new Event('homeloom:unauthorized'))
    expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  })

	it('exposes the redesigned workspace with semantic navigation and device list regions', async () => {
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		render(<App />)

		const navigation = await screen.findByRole('navigation', { name: '主要导航' })
		expect(navigation).toBeInTheDocument()
		expect(screen.getByRole('main')).toHaveAttribute('id', 'main-content')
		expect(screen.getByRole('button', { name: '设备' })).toHaveAttribute('aria-current', 'page')
		expect(screen.getAllByRole('button').filter((button) => button.getAttribute('aria-current') === 'page')).toHaveLength(1)
		expect(await screen.findByRole('region', { name: '设备列表' })).toBeInTheDocument()
		expect(screen.getByRole('status')).toHaveTextContent('0 / 0')
		expect(screen.queryByRole('button', { name: '管理家庭与房间' })).not.toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '设备来源' }))
		expect(await screen.findByRole('heading', { name: '设备来源管理' })).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '管理家庭与房间' }))
		expect(screen.getByRole('heading', { name: '家庭与房间' })).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '关闭' }))
	})

	it('filters the device center by source, model, home, and room', async () => {
		api.listProviders.mockResolvedValue([
			{ id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', enabled: true, status: 'running', capabilities: {}, config: {} },
			{ id: 'virtual-main', type: 'virtual', name: '虚拟来源', enabled: true, status: 'running', capabilities: {}, config: {} },
		])
		api.listDevices.mockResolvedValue([
			{ schemaVersion: 1, id: 'living-light', providerId: 'xiaomi-main', name: '客厅灯', type: 'lightbulb', homeId: 'home-a', homeName: '我的家', roomId: 'living', roomName: '客厅', availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-07-22T00:00:00Z' },
			{ schemaVersion: 1, id: 'bedroom-switch', providerId: 'xiaomi-main', name: '卧室开关', type: 'switch', homeId: 'home-a', homeName: '我的家', roomId: 'bedroom', roomName: '卧室', availability: 'offline', online: false, endpoints: [], lastUpdateAt: '2026-07-22T00:00:00Z' },
			{ schemaVersion: 1, id: 'parents-switch', providerId: 'virtual-main', name: '父母家开关', type: 'switch', homeId: 'home-b', homeName: '父母家', roomId: 'living', roomName: '客厅', availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-07-22T00:00:00Z' },
		])
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		render(<App />)
		const user = userEvent.setup()
		expect(await screen.findByRole('heading', { name: '客厅灯' })).toBeInTheDocument()

		await user.selectOptions(screen.getByLabelText('家庭筛选'), 'name:我的家')
		expect(screen.queryByRole('heading', { name: '父母家开关' })).not.toBeInTheDocument()
		await user.selectOptions(screen.getByLabelText('房间筛选'), 'name:我的家::name:客厅')
		expect(screen.getByRole('heading', { name: '客厅灯' })).toBeInTheDocument()
		expect(screen.queryByRole('heading', { name: '卧室开关' })).not.toBeInTheDocument()
		expect(screen.getByRole('status')).toHaveTextContent('1 / 3')

		await user.selectOptions(screen.getByLabelText('家庭筛选'), '')
		await user.selectOptions(screen.getByLabelText('设备来源筛选'), 'virtual-main')
		await user.selectOptions(screen.getByLabelText('统一模型筛选'), 'switch')
		expect(screen.getByRole('heading', { name: '父母家开关' })).toBeInTheDocument()
		expect(screen.queryByRole('heading', { name: '客厅灯' })).not.toBeInTheDocument()
	})

	it('requires exact phrases for Matter commissioning, Fabric deletion, and factory reset', async () => {
		api.listTargets.mockResolvedValue([{ id: 'matter-main', type: 'matter', name: 'Matter 主桥', enabled: true, status: 'running', deviceIds: [], devices: [], config: { commissioningWindowSeconds: 900 }, commissioning: { state: 'commissioned', windowOpen: false }, fabricCount: 1, endpointCount: 2, fabrics: [{ id: 'fabric-apple', label: 'Apple Home' }], certification: 'test' }])
		api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
		const prompt = vi.spyOn(window, 'prompt').mockReturnValueOnce('OPEN COMMISSIONING matter-main').mockReturnValueOnce('DELETE FABRIC matter-main fabric-apple').mockReturnValueOnce('FACTORY RESET matter-main')
		render(<App />)
		const user = userEvent.setup()
		await user.click(await screen.findByRole('button', { name: '桥接中心' }))
		await user.click(await screen.findByRole('button', { name: '打开配网窗口' }))
		await waitFor(() => expect(api.openMatterCommissioningWindow).toHaveBeenCalledWith('matter-main', 900, 'OPEN COMMISSIONING matter-main'))
		await user.click(screen.getByRole('button', { name: '删除 Fabric Apple Home' }))
		await waitFor(() => expect(api.deleteMatterFabric).toHaveBeenCalledWith('matter-main', 'fabric-apple', 'DELETE FABRIC matter-main fabric-apple'))
		await user.click(screen.getByRole('button', { name: '恢复 Matter 出厂身份' }))
		await waitFor(() => expect(api.factoryResetMatterTarget).toHaveBeenCalledWith('matter-main', 'FACTORY RESET matter-main'))
		expect(prompt.mock.calls.map((call) => call[0])).toEqual(expect.arrayContaining([expect.stringContaining('OPEN COMMISSIONING matter-main'), expect.stringContaining('DELETE FABRIC matter-main fabric-apple'), expect.stringContaining('FACTORY RESET matter-main')]))
	})
})
