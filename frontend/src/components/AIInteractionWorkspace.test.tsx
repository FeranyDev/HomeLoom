import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AIInteractionWorkspace } from './AIInteractionWorkspace'
import type { Device } from '../types/device'

afterEach(() => { cleanup(); sessionStorage.clear(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

const devices: Device[] = [{
  schemaVersion: 1, id: 'living-light', providerId: 'virtual-main', name: '客厅灯', type: 'lightbulb', availability: 'online', online: true, lastUpdateAt: '2026-08-23T00:00:00Z',
  endpoints: [{ id: 'main', name: '主端点', type: 'light', capabilities: [{ id: 'switch', type: 'switch', properties: [{ definition: { id: 'power', name: '电源', type: 'bool', readable: true, writable: true, notifiable: true }, value: { type: 'bool', bool: false } }] }, { id: 'brightness', type: 'brightness', properties: [{ definition: { id: 'level', name: '亮度', type: 'number', readable: true, writable: true, notifiable: true }, value: { type: 'number', number: 50 } }] }] }],
}]

function response(data: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify({ data }), { status, headers: { 'Content-Type': 'application/json' } })) }
function stream(run: unknown) { return Promise.resolve(new Response(`event: delta\ndata: {"type":"delta","delta":"正在准备操作。"}\n\nevent: run\ndata: {"type":"run","run":${JSON.stringify(run)}}\n\n`, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })) }

describe('AIInteractionWorkspace', () => {
  it('provides direct AI interaction with an explicit device-operation approval step', async () => {
    const fetchMock = vi.fn().mockImplementation((path: string, init?: RequestInit) => {
      if (path === '/api/v1/ai-service/automations') return response([])
      if (path === '/api/v1/ai-service/runs/stream' && init?.method === 'POST') return stream({ id: 'run-1', status: 'awaiting_approval', message: '等待批准', createdAt: '2026-08-23T00:00:00Z', action: { deviceId: 'living-light', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', value: { type: 'bool', bool: true }, deviceName: '客厅灯', propertyName: '电源' } })
      if (path === '/api/v1/ai-service/runs/run-1/approve') return response({ id: 'run-1', status: 'executed', message: '已执行', createdAt: '2026-08-23T00:00:00Z' })
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<AIInteractionWorkspace devices={devices} />)

    await screen.findByText('尚未配置自动任务。')
    await userEvent.type(screen.getByLabelText('向 AI 发送消息'), '打开客厅灯')
    await userEvent.hover(screen.getByRole('button', { name: 'AI 请求说明' }))
    expect(screen.getByRole('tooltip')).toHaveTextContent('复杂分析最长约 6 分钟；取消会中断本次请求，且不会执行设备操作。')
    await userEvent.click(screen.getByRole('button', { name: '发送给 AI' }))
    expect(await screen.findByRole('button', { name: '批准设备操作' })).toBeInTheDocument()
    expect(screen.getByText('客厅灯')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '批准设备操作' }))
    expect(await screen.findByText('已执行', { selector: 'p' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/ai-service/runs/run-1/approve', expect.objectContaining({ method: 'POST' }))
    const streamRequest = fetchMock.mock.calls.find(([path]) => path === '/api/v1/ai-service/runs/stream')?.[1] as RequestInit
    expect(JSON.parse(String(streamRequest.body))).toEqual({ message: '打开客厅灯', history: [] })
  })

  it('saves scheduled and state-triggered task configurations', async () => {
    const fetchMock = vi.fn().mockImplementation((path: string, init?: RequestInit) => {
      if (path === '/api/v1/ai-service/automations' && !init?.method) return response([])
      if (path === '/api/v1/ai-service/automations' && init?.method === 'POST') return response({ id: 'ai-task-1', name: '每日巡检', enabled: true, kind: 'schedule', prompt: '检查状态', intervalSeconds: 300, createdAt: '2026-08-23T00:00:00Z', updatedAt: '2026-08-23T00:00:00Z' }, 201)
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<AIInteractionWorkspace devices={devices} />)

    await screen.findByText('尚未配置自动任务。')
    await userEvent.type(screen.getByLabelText('自动任务名称'), '每日巡检')
    await userEvent.type(screen.getByLabelText('自动任务提示词'), '检查状态')
    await userEvent.click(screen.getByRole('button', { name: '添加判断条件' }))
    await userEvent.selectOptions(screen.getByLabelText('判断条件 1 属性'), ['living-light', 'main', 'switch', 'power'].join('\u0000'))
    await userEvent.selectOptions(screen.getByLabelText('判断条件 1 值'), 'false')
    await userEvent.click(screen.getByRole('button', { name: '添加判断条件' }))
    await userEvent.selectOptions(screen.getByLabelText('判断条件 2 属性'), ['living-light', 'main', 'brightness', 'level'].join('\u0000'))
    await userEvent.selectOptions(screen.getByLabelText('判断条件关系'), 'any')
    await userEvent.click(screen.getByRole('button', { name: '保存自动任务' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/ai-service/automations', expect.objectContaining({ method: 'POST' })))
    const saveRequest = fetchMock.mock.calls.find(([path, init]) => path === '/api/v1/ai-service/automations' && init?.method === 'POST')?.[1] as RequestInit
    expect(saveRequest.body).toContain('"kind":"schedule"')
    expect(saveRequest.body).toContain('"executionMode":"unattended"')
    expect(saveRequest.body).toContain('"conditions":[{"deviceId":"living-light"')
    expect(saveRequest.body).toContain('"operator":"equals"')
    expect(saveRequest.body).toContain('"conditionMatch":"any"')
    expect(await screen.findByText('每日巡检')).toBeInTheDocument()
  })

  it('shows persisted automation history and unattended execution results', async () => {
    const task = { id: 'ai-task-1', name: '夜间节能', enabled: true, kind: 'schedule', prompt: '如有需要关闭客厅灯', executionMode: 'unattended', intervalSeconds: 300, createdAt: '2026-08-23T00:00:00Z', updatedAt: '2026-08-23T00:00:00Z' }
    const executed = { id: 'run-task-1', source: 'manual', status: 'executed', message: '已完成设备检查。', autoApproved: true, createdAt: '2026-08-23T00:05:00Z', action: { deviceId: 'living-light', endpointId: 'main', capabilityId: 'switch', propertyId: 'power', value: { type: 'bool', bool: false }, deviceName: '客厅灯', propertyName: '电源' } }
    const fetchMock = vi.fn().mockImplementation((path: string, init?: RequestInit) => {
      if (path === '/api/v1/ai-service/automations' && !init?.method) return response([{ ...task, runHistory: [executed] }])
      if (path === '/api/v1/ai-service/automations/ai-task-1/run' && init?.method === 'POST') return response({ automation: { ...task, lastRunId: executed.id, lastRunStatus: 'executed', lastRunMessage: executed.message, lastRunAt: executed.createdAt, runHistory: [executed] }, run: executed })
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<AIInteractionWorkspace devices={devices} />)

    await screen.findByText('夜间节能')
    await userEvent.click(screen.getByText('运行记录（保留最近 1 / 50 条）'))
    expect(await screen.findByText('已完成设备检查。')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '立即运行' }))
    expect(await screen.findByText('已手动启动自动任务；AI 生成的设备操作已按无人值守策略自动批准。')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '批准设备操作' })).not.toBeInTheDocument()
  })

  it('saves fixed household-time and Cron schedules', async () => {
    const fetchMock = vi.fn().mockImplementation((path: string, init?: RequestInit) => {
      if (path === '/api/v1/ai-service/automations' && !init?.method) return response([])
      if (path === '/api/v1/ai-service/automations' && init?.method === 'POST') return response({ id: 'ai-task-cron', name: '每日巡检', enabled: true, kind: 'schedule', prompt: '检查状态', cronExpression: '30 8 * * *', createdAt: '2026-08-23T00:00:00Z', updatedAt: '2026-08-23T00:00:00Z' }, 201)
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<AIInteractionWorkspace devices={devices} />)
    await screen.findByText('尚未配置自动任务。')
    await userEvent.type(screen.getByLabelText('自动任务名称'), '每日巡检')
    await userEvent.type(screen.getByLabelText('自动任务提示词'), '检查状态')
    await userEvent.selectOptions(screen.getByLabelText('定时执行方式'), 'fixed')
    await userEvent.clear(screen.getByLabelText('每天固定执行时间'))
    await userEvent.type(screen.getByLabelText('每天固定执行时间'), '08:30')
    await userEvent.click(screen.getByRole('button', { name: '保存自动任务' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/ai-service/automations', expect.objectContaining({ method: 'POST' })))
    const saved = fetchMock.mock.calls.find(([path, init]) => path === '/api/v1/ai-service/automations' && init?.method === 'POST')?.[1] as RequestInit
    expect(JSON.parse(String(saved.body)).cronExpression).toBe('30 8 * * *')
    expect(await screen.findByText(/每天 08:30（家庭时区）/)).toBeInTheDocument()
  })
})
