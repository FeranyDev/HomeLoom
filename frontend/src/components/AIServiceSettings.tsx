import { useEffect, useState } from 'react'
import { getAIServiceConfig, listAIModels, saveAIServiceConfig } from '../api/ai'
import type { AIAPIProtocol, AIHomePreferences, AIModel, AISessionContextSettings, AIServiceStatus } from '../types/ai'
import { HelpTooltip } from './HelpTooltip'

const defaultSessionContext: AISessionContextSettings = { enabled: true, currentTime: true, timeZone: true, weekday: true, runSource: true, triggerState: true, regionLanguage: true, temperatureUnit: true }
const defaultHomePreferences: AIHomePreferences = { timeZone: 'Asia/Shanghai', regionLanguage: 'zh-CN', temperatureUnit: 'celsius' }
const emptyStatus: AIServiceStatus = { apiBaseUrl: 'https://api.openai.com/v1', apiProxyUrl: '', model: '', apiProtocol: 'responses', agentInstructions: '', defaultAgentInstructions: '', sessionContext: defaultSessionContext, homePreferences: defaultHomePreferences, apiKeyConfigured: false, configured: false }

function normalizedSessionContext(value?: Partial<AISessionContextSettings>): AISessionContextSettings { return { ...defaultSessionContext, ...value } }
function normalizedHomePreferences(value?: Partial<AIHomePreferences>): AIHomePreferences { return { ...defaultHomePreferences, ...value } }

const providerPresets = [
  { id: 'openai', label: 'OpenAI', apiBaseUrl: 'https://api.openai.com/v1', apiProtocol: 'responses' },
  { id: 'deepseek', label: 'DeepSeek', apiBaseUrl: 'https://api.deepseek.com', apiProtocol: 'chat-completions' },
  { id: 'openrouter', label: 'OpenRouter', apiBaseUrl: 'https://openrouter.ai/api/v1', apiProtocol: 'chat-completions' },
  { id: 'qwen', label: '阿里云百炼（通义）', apiBaseUrl: 'https://dashscope.aliyuncs.com/compatible-mode/v1', apiProtocol: 'chat-completions' },
  { id: 'gemini', label: 'Google Gemini（兼容模式）', apiBaseUrl: 'https://generativelanguage.googleapis.com/v1beta/openai', apiProtocol: 'chat-completions' },
  { id: 'groq', label: 'Groq', apiBaseUrl: 'https://api.groq.com/openai/v1', apiProtocol: 'chat-completions' },
  { id: 'mistral', label: 'Mistral AI', apiBaseUrl: 'https://api.mistral.ai/v1', apiProtocol: 'chat-completions' },
  { id: 'xai', label: 'xAI', apiBaseUrl: 'https://api.x.ai/v1', apiProtocol: 'chat-completions' },
] as const

type ProviderPresetID = typeof providerPresets[number]['id'] | 'custom'

function providerPresetID(apiBaseUrl: string, apiProtocol: AIAPIProtocol): ProviderPresetID {
  return providerPresets.find((preset) => preset.apiBaseUrl === apiBaseUrl && preset.apiProtocol === apiProtocol)?.id ?? 'custom'
}

export function AIServiceSettings() {
  const [status, setStatus] = useState<AIServiceStatus | null>(null)
  const [apiBaseUrl, setAPIBaseURL] = useState(emptyStatus.apiBaseUrl)
  const [apiProxyUrl, setAPIProxyURL] = useState(emptyStatus.apiProxyUrl)
  const [apiKey, setAPIKey] = useState('')
  const [clearAPIKey, setClearAPIKey] = useState(false)
  const [model, setModel] = useState('')
  const [apiProtocol, setAPIProtocol] = useState<AIAPIProtocol>(emptyStatus.apiProtocol)
  const [agentInstructions, setAgentInstructions] = useState(emptyStatus.agentInstructions)
  const [sessionContext, setSessionContext] = useState<AISessionContextSettings>(defaultSessionContext)
  const [homePreferences, setHomePreferences] = useState<AIHomePreferences>(defaultHomePreferences)
  const [provider, setProvider] = useState<ProviderPresetID>('openai')
  const [models, setModels] = useState<AIModel[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [loadingModels, setLoadingModels] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    void getAIServiceConfig().then((next) => {
      if (!active) return
      setStatus(next); setAPIBaseURL(next.apiBaseUrl); setAPIProxyURL(next.apiProxyUrl ?? ''); setModel(next.model); setAPIProtocol(next.apiProtocol); setAgentInstructions(next.agentInstructions || next.defaultAgentInstructions || ''); setSessionContext(normalizedSessionContext(next.sessionContext)); setHomePreferences(normalizedHomePreferences(next.homePreferences)); setProvider(providerPresetID(next.apiBaseUrl, next.apiProtocol)); setError(null)
    }).catch((cause) => {
      if (active) setError(cause instanceof Error ? cause.message : '读取 AI 服务配置失败')
    }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [])

  async function save() {
    if (!apiBaseUrl.trim()) { setError('请输入 AI API 地址'); return }
    if (!clearAPIKey && !status?.apiKeyConfigured && !apiKey.trim()) { setError('首次保存需要输入 AI API 密钥'); return }
    setSaving(true); setError(null); setMessage(null)
    try {
      const next = await saveAIServiceConfig({ apiBaseUrl, apiProxyUrl, apiKey: apiKey || undefined, clearApiKey: clearAPIKey || undefined, model, apiProtocol, agentInstructions, sessionContext, homePreferences })
      setStatus(next); setAPIBaseURL(next.apiBaseUrl); setAPIProxyURL(next.apiProxyUrl ?? ''); setModel(next.model); setAPIProtocol(next.apiProtocol); setAgentInstructions(next.agentInstructions || next.defaultAgentInstructions || ''); setSessionContext(normalizedSessionContext(next.sessionContext)); setHomePreferences(normalizedHomePreferences(next.homePreferences)); setProvider(providerPresetID(next.apiBaseUrl, next.apiProtocol)); setAPIKey(''); setClearAPIKey(false)
      setMessage(next.configured ? 'AI 服务已保存并实时启用。' : '连接信息已保存；从接口获取模型后选择一个模型即可启用。')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存 AI 服务配置失败')
    } finally { setSaving(false) }
  }

  async function loadModels() {
    setLoadingModels(true); setError(null); setMessage(null)
    try {
      const next = await listAIModels()
      setModels(next)
      setMessage(next.length ? `已从 API 获取 ${next.length} 个模型。` : '接口未返回可用模型。')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '获取模型列表失败；请先保存 API 地址和密钥。')
    } finally { setLoadingModels(false) }
  }

  function selectProvider(id: ProviderPresetID) {
    setProvider(id)
    const preset = providerPresets.find((item) => item.id === id)
    if (!preset) return
    setAPIBaseURL(preset.apiBaseUrl)
    setAPIProtocol(preset.apiProtocol)
    setError(null)
    setMessage('已填入服务地址和协议；请保存后获取模型列表。')
  }

  if (loading) return <div className="queue-card settings-card" role="status">正在读取 AI 服务配置…</div>
  return <section className="queue-card settings-card ai-service-settings" aria-label="AI 服务配置">
    <div><span><HelpTooltip label="AI 服务说明" content="支持 Responses 或 Chat Completions 兼容服务。密钥不写入 HomeLoom 数据库；已设置密钥时留空即可保留。">AI 服务</HelpTooltip></span><strong>{status?.configured ? '已启用' : status?.apiKeyConfigured ? '等待选择模型' : '未配置'}</strong></div>

    <label><HelpTooltip label="预设说明" content="预设只会填写服务地址和协议，不会改动密钥或模型。">服务预设</HelpTooltip><select aria-label="AI 服务预设" value={provider} onChange={(event) => selectProvider(event.target.value as ProviderPresetID)}><option value="custom">自定义兼容服务</option>{providerPresets.map((preset) => <option key={preset.id} value={preset.id}>{preset.label}</option>)}</select></label>


    <label>API 地址<input aria-label="AI API 地址" type="url" value={apiBaseUrl} onChange={(event) => { setAPIBaseURL(event.target.value); setProvider('custom') }} placeholder="https://api.example.com/v1" autoComplete="url" /></label>
    <label><HelpTooltip label="代理说明" content="仅支持无认证的 HTTP/HTTPS 正向代理；留空则不使用代理。">代理</HelpTooltip><input aria-label="网络代理" type="url" value={apiProxyUrl} onChange={(event) => setAPIProxyURL(event.target.value)} placeholder="http://127.0.0.1:7890" autoComplete="url" /></label>

    <label><HelpTooltip label="协议兼容性说明" content="Responses 使用 /responses，Chat Completions 使用 /chat/completions。模型列表依赖 /models；不支持时可手动填写。">API 协议</HelpTooltip><select aria-label="AI API 协议" value={apiProtocol} onChange={(event) => { setAPIProtocol(event.target.value as AIAPIProtocol); setProvider('custom') }}><option value="responses">Responses API</option><option value="chat-completions">Chat Completions</option></select></label>
    <label><HelpTooltip label="OpenAI API 密钥说明" content="Codex 或 ChatGPT 订阅不能代替 OpenAI API 密钥。">API 密钥</HelpTooltip><input aria-label="AI API 密钥" type="password" value={apiKey} disabled={clearAPIKey} onChange={(event) => setAPIKey(event.target.value)} placeholder={status?.apiKeyConfigured ? '已设置；留空保持不变' : '首次保存必填'} autoComplete="new-password" /></label>
    {status?.apiKeyConfigured && <label className="ai-task-enabled"><input aria-label="移除已保存的 AI API 密钥" type="checkbox" checked={clearAPIKey} onChange={(event) => { setClearAPIKey(event.target.checked); if (event.target.checked) setAPIKey('') }} />清除已保存的 API 密钥</label>}
    <label>模型<input aria-label="AI 模型" list="ai-service-models" value={model} onChange={(event) => setModel(event.target.value)} placeholder="可手动填写或从接口选择" /><datalist id="ai-service-models">{models.map((item) => <option key={item.id} value={item.id} />)}</datalist></label>
    <label className="ai-agent-instructions"><HelpTooltip label="回复规则说明" content="回复规则会随配置保存。设备授权、状态校验和人工批准始终由系统执行。">回复规则</HelpTooltip><textarea aria-label="智能体提示词" value={agentInstructions} onChange={(event) => setAgentInstructions(event.target.value)} maxLength={16384} spellCheck={false} /></label>

    <fieldset className="ai-home-preferences">
      <legend><HelpTooltip label="家庭偏好说明" content="时区用于计算当前时间和星期；地区语言和温度单位可按下方开关提供给模型。">家庭偏好</HelpTooltip></legend>
      <label>家庭默认时区<input aria-label="家庭默认时区" value={homePreferences.timeZone} onChange={(event) => setHomePreferences((value) => ({ ...value, timeZone: event.target.value }))} list="ai-home-time-zones" placeholder="Asia/Shanghai" /><datalist id="ai-home-time-zones"><option value="Asia/Shanghai" /><option value="Asia/Tokyo" /><option value="Asia/Singapore" /><option value="Europe/London" /><option value="Europe/Berlin" /><option value="America/New_York" /><option value="America/Los_Angeles" /><option value="UTC" /></datalist></label>
      <label>地区语言<input aria-label="家庭地区语言" value={homePreferences.regionLanguage} onChange={(event) => setHomePreferences((value) => ({ ...value, regionLanguage: event.target.value }))} placeholder="zh-CN" /></label>
      <label>温度单位<select aria-label="家庭温度单位" value={homePreferences.temperatureUnit} onChange={(event) => setHomePreferences((value) => ({ ...value, temperatureUnit: event.target.value as AIHomePreferences['temperatureUnit'] }))}><option value="celsius">摄氏度（°C）</option><option value="fahrenheit">华氏度（°F）</option></select></label>
    </fieldset>
    <fieldset className="ai-session-context">
      <legend><HelpTooltip label="会话上下文说明" content="此开关同时作用于网页对话和自动任务。关闭后不会提供以下运行信息或家庭偏好。">会话上下文</HelpTooltip></legend>
      <label className="ai-task-enabled"><input aria-label="启用会话上下文注入" type="checkbox" checked={sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, enabled: event.target.checked }))} />启用上下文</label>

      <div className="ai-session-context-options">
        <label><input aria-label="注入当前时间" type="checkbox" checked={sessionContext.currentTime} disabled={!sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, currentTime: event.target.checked }))} />当前时间</label>
        <label><input aria-label="注入时区" type="checkbox" checked={sessionContext.timeZone} disabled={!sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, timeZone: event.target.checked }))} />时区（家庭默认）</label>
        <label><input aria-label="注入星期" type="checkbox" checked={sessionContext.weekday} disabled={!sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, weekday: event.target.checked }))} />星期</label>
        <label><input aria-label="注入运行来源" type="checkbox" checked={sessionContext.runSource} disabled={!sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, runSource: event.target.checked }))} />运行来源</label>
        <label><input aria-label="注入状态触发快照" type="checkbox" checked={sessionContext.triggerState} disabled={!sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, triggerState: event.target.checked }))} />状态触发快照</label>
        <label><input aria-label="会话注入地区语言" type="checkbox" checked={sessionContext.regionLanguage} disabled={!sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, regionLanguage: event.target.checked }))} />地区语言</label>
        <label><input aria-label="会话注入温度单位" type="checkbox" checked={sessionContext.temperatureUnit} disabled={!sessionContext.enabled} onChange={(event) => setSessionContext((value) => ({ ...value, temperatureUnit: event.target.checked }))} />温度单位</label>
      </div>
    </fieldset>
    <div className="ai-service-actions"><button type="button" onClick={() => void loadModels()} disabled={loadingModels || !status?.apiKeyConfigured}>{loadingModels ? '获取中…' : '获取模型'}</button><button type="button" onClick={() => { setAgentInstructions(status?.defaultAgentInstructions ?? ''); setMessage('已恢复默认提示词；请保存后生效。'); setError(null) }}>恢复默认</button><button className="primary" type="button" onClick={() => void save()} disabled={saving}>{saving ? '保存中…' : '保存'}</button></div>
    {error && <small className="field-error" role="alert">{error}</small>}{message && <small className="maintenance-message" role="status">{message}</small>}

  </section>
}
