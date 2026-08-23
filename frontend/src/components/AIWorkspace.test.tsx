import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AIWorkspace } from './AIWorkspace'
import type { Device } from '../types/device'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

const devices: Device[] = [
  { schemaVersion: 1, id: 'living-light', providerId: 'virtual-main', name: '客厅灯', type: 'lightbulb', availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-08-23T00:00:00Z' },
  { schemaVersion: 1, id: 'bedroom-switch', providerId: 'virtual-main', name: '卧室开关', type: 'switch', availability: 'online', online: true, endpoints: [], lastUpdateAt: '2026-08-23T00:00:00Z' },
]

describe('AIWorkspace', () => {
  it('keeps AI service setup and all device authorization in the AI page', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((path: string) => {
      if (path === '/api/v1/ai-service/config') return Promise.resolve(new Response(JSON.stringify({ data: { apiBaseUrl: 'https://api.example.test/v1', model: '', apiProtocol: 'responses', agentInstructions: '默认提示词', defaultAgentInstructions: '默认提示词', apiKeyConfigured: false, configured: false } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      return Promise.resolve(new Response(JSON.stringify({ data: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    }))
    render(<AIWorkspace devices={devices} />)

    expect(await screen.findByRole('region', { name: 'AI 服务配置' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '设备与已绑定属性' })).toBeInTheDocument()
    expect(screen.getByText('客厅灯')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('筛选 AI 授权设备'), '卧室')
    expect(screen.queryByText('客厅灯')).not.toBeInTheDocument()
    expect(screen.getByText('卧室开关')).toBeInTheDocument()
  })
})
