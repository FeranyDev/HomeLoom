import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'

const api = vi.hoisted(() => ({
  getAuthStatus: vi.fn(), login: vi.fn(), logout: vi.fn(), setupAdministrator: vi.fn(),
  listDevices: vi.fn(), setDeviceEnabled: vi.fn(), setDevicePower: vi.fn(), setDeviceProperty: vi.fn(), simulateDevice: vi.fn(), executeDeviceCommand: vi.fn(),
  listTargets: vi.fn(), saveTarget: vi.fn(), deleteTarget: vi.fn(), regenerateTargetPairing: vi.fn(), clearTargetPairingIdentity: vi.fn(),
  listProviders: vi.fn(), saveProvider: vi.fn(), deleteProvider: vi.fn(), restartProvider: vi.fn(), testProviderConnection: vi.fn(),
  getDiagnostics: vi.fn(), getRuntimeSettings: vi.fn(), listAuditEvents: vi.fn(), listCommands: vi.fn(), saveRuntimeSettings: vi.fn(),
  getSystemVersion: vi.fn(),
  listModelContracts: vi.fn(), listCustomModelProperties: vi.fn(),
}))

vi.mock('./api/auth', () => ({ getAuthStatus: api.getAuthStatus, login: api.login, logout: api.logout, setupAdministrator: api.setupAdministrator }))
vi.mock('./api/devices', () => ({
  listDevices: api.listDevices, setDeviceEnabled: api.setDeviceEnabled, setDevicePower: api.setDevicePower, setDeviceProperty: api.setDeviceProperty, simulateDevice: api.simulateDevice, executeDeviceCommand: api.executeDeviceCommand,
  subscribeDevices: (_handler: unknown, onStatus: (live: boolean) => void) => { onStatus(true); return () => {} },
}))
vi.mock('./api/targets', () => ({
  listTargets: api.listTargets, saveTarget: api.saveTarget, deleteTarget: api.deleteTarget, regenerateTargetPairing: api.regenerateTargetPairing, clearTargetPairingIdentity: api.clearTargetPairingIdentity,
  subscribeTargets: () => () => {},
}))
vi.mock('./api/providers', () => ({ listProviders: api.listProviders, saveProvider: api.saveProvider, deleteProvider: api.deleteProvider, restartProvider: api.restartProvider, testProviderConnection: api.testProviderConnection }))
vi.mock('./api/diagnostics', () => ({
  getDiagnostics: api.getDiagnostics, getRuntimeSettings: api.getRuntimeSettings, listAuditEvents: api.listAuditEvents, listCommands: api.listCommands, saveRuntimeSettings: api.saveRuntimeSettings,
  subscribeAuditEvents: () => () => {}, subscribeCommands: () => () => {},
}))
vi.mock('./api/system', () => ({ getSystemVersion: api.getSystemVersion }))
vi.mock('./api/mapping', async (importOriginal) => {
  const original = await importOriginal<typeof import('./api/mapping')>()
  return { ...original, listModelContracts: api.listModelContracts, listCustomModelProperties: api.listCustomModelProperties }
})

beforeEach(() => {
  vi.clearAllMocks()
  window.history.replaceState(null, '', '#/devices')
  api.listDevices.mockResolvedValue([])
  api.listTargets.mockResolvedValue([])
  api.listProviders.mockResolvedValue([])
  api.getDiagnostics.mockResolvedValue({ eventsProcessed: 0 })
  api.listCommands.mockResolvedValue([])
  api.listAuditEvents.mockResolvedValue([])
  api.getSystemVersion.mockResolvedValue({ version: 'test', commit: 'abc', buildTime: 'now' })
  api.getRuntimeSettings.mockResolvedValue(null)
  api.listModelContracts.mockResolvedValue(Array.from({ length: 27 }, (_, index) => ({ deviceType: `model-${index + 1}`, name: `模型 ${index + 1}`, version: 1, builtIn: true, parameters: [], custom: { publisher: { level: 'custom', behavior: 'preserve-and-mark-custom' }, consumer: { level: 'custom', behavior: 'explicit-path-mapping-only' } } })))
  api.listCustomModelProperties.mockResolvedValue([])
  api.logout.mockResolvedValue(undefined)
})

describe('App integration', () => {
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
    expect(screen.getByText('还没有目标实例')).toBeInTheDocument()
	await user.click(screen.getByRole('button', { name: '设备来源' }))
	expect(await screen.findByRole('heading', { name: '设备来源管理' })).toBeInTheDocument()
	expect(screen.getByRole('button', { name: '＋ 新建设备来源' })).toBeInTheDocument()
    expect(screen.getByText('还没有 Provider')).toBeInTheDocument()
	expect(screen.queryByRole('button', { name: '米家' })).not.toBeInTheDocument()
	expect(screen.getByText('一种生命周期，接入所有数据源。')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '退出' }))
    await waitFor(() => expect(api.logout).toHaveBeenCalledOnce())
    expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  })

  it('reads the unified model summary count from the model catalog', async () => {
    api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
    render(<App />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '统一模型' }))
    expect(await screen.findByLabelText('27 统一设备模型')).toBeInTheDocument()
    expect(api.listModelContracts).toHaveBeenCalled()
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

		await user.selectOptions(screen.getByLabelText('家庭筛选'), 'id:home-a')
		expect(screen.queryByRole('heading', { name: '父母家开关' })).not.toBeInTheDocument()
		await user.selectOptions(screen.getByLabelText('房间筛选'), 'id:home-a::id:living')
		expect(screen.getByRole('heading', { name: '客厅灯' })).toBeInTheDocument()
		expect(screen.queryByRole('heading', { name: '卧室开关' })).not.toBeInTheDocument()
		expect(screen.getByRole('status')).toHaveTextContent('1 / 3')

		await user.selectOptions(screen.getByLabelText('家庭筛选'), '')
		await user.selectOptions(screen.getByLabelText('设备来源筛选'), 'virtual-main')
		await user.selectOptions(screen.getByLabelText('统一模型筛选'), 'switch')
		expect(screen.getByRole('heading', { name: '父母家开关' })).toBeInTheDocument()
		expect(screen.queryByRole('heading', { name: '客厅灯' })).not.toBeInTheDocument()
	})
})
