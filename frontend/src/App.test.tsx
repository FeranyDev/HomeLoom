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
    expect(screen.getByText('还没有桥')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Provider' }))
    expect(await screen.findByRole('heading', { name: 'Provider 管理' })).toBeInTheDocument()
    expect(screen.getByText('还没有 Provider')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '退出' }))
    await waitFor(() => expect(api.logout).toHaveBeenCalledOnce())
    expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  })

  it('returns an authenticated dashboard to login on a global unauthorized event', async () => {
    api.getAuthStatus.mockResolvedValue({ initialized: true, authenticated: true, username: 'admin' })
    render(<App />)
    await screen.findByRole('button', { name: '退出' })
    window.dispatchEvent(new Event('homeloom:unauthorized'))
    expect(await screen.findByRole('button', { name: '登录' })).toBeInTheDocument()
  })
})
