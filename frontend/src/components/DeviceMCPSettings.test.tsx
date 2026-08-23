import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DeviceMCPSettings } from './DeviceMCPSettings'
import type { Device } from '../types/device'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

const device: Device = {
  schemaVersion: 1, id: 'switch-1', providerId: 'virtual-main', name: '客厅开关', type: 'switch', availability: 'online', online: true, lastUpdateAt: '2026-08-23T00:00:00Z',
  endpoints: [{ id: 'main', name: '主端点', type: 'switch', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: '开关', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } }] }] }],
}

describe('DeviceMCPSettings', () => {
  it('persists device authorization and a bound-property note separately', async () => {
    const fetchMock = vi.fn().mockImplementation((path: string, init?: RequestInit) => {
      if (init?.method === 'PUT' && path.endsWith('/mcp-config')) return Promise.resolve(new Response(JSON.stringify({ data: { deviceId: 'switch-1', enabled: true, usageNote: '客厅照明', defaultAccess: 'read' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      if (init?.method === 'PUT') return Promise.resolve(new Response(JSON.stringify({ data: { deviceId: 'switch-1', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', usageNote: '睡眠时不要打开', access: 'confirm' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      if (path.endsWith('/mcp-config')) return Promise.resolve(new Response(JSON.stringify({ data: { deviceId: 'switch-1', enabled: false, usageNote: '', defaultAccess: 'hidden' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      return Promise.resolve(new Response(JSON.stringify({ data: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<DeviceMCPSettings device={device} />)

    await screen.findByRole('button', { name: '保存设备授权' })
    await userEvent.click(screen.getByLabelText('启用该设备的 MCP'))
    await userEvent.selectOptions(screen.getByLabelText('设备默认 MCP 权限'), 'read')
    await userEvent.type(screen.getByLabelText('设备 MCP 使用备注'), '客厅照明')
    await userEvent.click(screen.getByRole('button', { name: '保存设备授权' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/devices/switch-1/mcp-config', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ enabled: true, usageNote: '客厅照明', defaultAccess: 'read' }) })))

    await userEvent.selectOptions(screen.getByLabelText('开关 MCP 权限'), 'confirm')
    fireEvent.change(screen.getByLabelText('开关 MCP 使用备注'), { target: { value: '睡眠时不要打开' } })
    await userEvent.click(screen.getByRole('button', { name: '保存属性配置' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/devices/switch-1/mcp-properties/main/switch/power', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ usageNote: '睡眠时不要打开', access: 'confirm' }) })))
  })
})
