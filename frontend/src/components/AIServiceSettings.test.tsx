import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AIServiceSettings } from './AIServiceSettings'

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('AIServiceSettings', () => {
  it('keeps the API key out of rendered configuration and loads models from the service API', async () => {
    const fetchMock = vi.fn().mockImplementation((path: string, init?: RequestInit) => {
      if (path === '/api/v1/ai-service/config' && !init?.method) return Promise.resolve(new Response(JSON.stringify({ data: { apiBaseUrl: 'https://models.example.test/v1', apiProxyUrl: '', model: '', apiProtocol: 'responses', agentInstructions: '默认安全提示词', defaultAgentInstructions: '默认安全提示词', apiKeyConfigured: false, configured: false } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      if (path === '/api/v1/ai-service/config' && init?.method === 'PUT') return Promise.resolve(new Response(JSON.stringify({ data: { apiBaseUrl: 'https://models.example.test/v1', apiProxyUrl: 'http://127.0.0.1:7890', model: '', apiProtocol: 'responses', agentInstructions: '自定义回复规则', defaultAgentInstructions: '默认安全提示词', apiKeyConfigured: true, configured: false } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      if (path === '/api/v1/ai-service/models') return Promise.resolve(new Response(JSON.stringify({ data: [{ id: 'model-a' }, { id: 'model-b' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<AIServiceSettings />)
    await screen.findByText('未配置')
    await userEvent.type(screen.getByLabelText('AI API 密钥'), 'secret-api-key')
    await userEvent.type(screen.getByLabelText('网络代理'), 'http://127.0.0.1:7890')
    await userEvent.clear(screen.getByLabelText('智能体提示词'))
    await userEvent.type(screen.getByLabelText('智能体提示词'), '自定义回复规则')
    await userEvent.click(screen.getByRole('button', { name: '保存 AI 服务配置' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/ai-service/config', expect.objectContaining({ method: 'PUT' })))
    const saveRequest = fetchMock.mock.calls.find(([path, init]) => path === '/api/v1/ai-service/config' && init?.method === 'PUT')?.[1] as RequestInit
    expect(saveRequest.body).toContain('secret-api-key')
    expect(saveRequest.body).toContain('"apiProxyUrl":"http://127.0.0.1:7890"')
    expect(saveRequest.body).toContain('"apiProtocol":"responses"')
    expect(saveRequest.body).toContain('"agentInstructions":"自定义回复规则"')
    expect(screen.queryByDisplayValue('secret-api-key')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '恢复默认提示词' }))
    expect(screen.getByLabelText('智能体提示词')).toHaveValue('默认安全提示词')
    expect(await screen.findByText('已恢复默认提示词；请保存后生效。')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: '从接口获取模型' }))
    await screen.findByText('已从 API 获取 2 个模型。')
    expect(document.querySelector('#ai-service-models option[value="model-a"]')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('AI 模型'), 'model-a')
    expect(screen.getByLabelText('AI 模型')).toHaveValue('model-a')

    await userEvent.selectOptions(screen.getByLabelText('AI 服务预设'), 'deepseek')
    expect(screen.getByLabelText('AI API 地址')).toHaveValue('https://api.deepseek.com')
    expect(screen.getByLabelText('AI API 协议')).toHaveValue('chat-completions')
    expect(screen.getByText('预设只会填入服务地址和协议，不会变更密钥或模型。')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('AI 服务预设'), 'groq')
    expect(screen.getByLabelText('AI API 地址')).toHaveValue('https://api.groq.com/openai/v1')
    expect(screen.getByText('Codex/ChatGPT 订阅登录不能代替 API Key；如需 OpenAI 模型，请使用单独开通的 OpenAI API Key。')).toBeInTheDocument()
  })
})
