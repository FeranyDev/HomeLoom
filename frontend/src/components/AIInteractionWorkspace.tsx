import { useEffect, useMemo, useState } from 'react'
import { approveAIAutomationRun, approveAIRun, createAIAutomation, deleteAIAutomation, listAIAutomations, runAIAutomation, saveAIAutomation, startAIRunStream } from '../api/ai'
import type { AIAutomation, AIAutomationCondition, AIAutomationConditionMatch, AIAutomationConditionOperator, AIAutomationExecutionMode, AIAutomationInput, AIAutomationKind, AIConversationTurn, AIRun, AIRunAction } from '../types/ai'
import type { Device, PropertyDefinition, PropertyValue, ValueType } from '../types/device'
import { MarkdownMessage } from './MarkdownMessage'

interface PropertyTarget {
  key: string
  deviceId: string
  deviceName: string
  endpointId: string
  capabilityId: string
  propertyId: string
  definition: PropertyDefinition
  value: PropertyValue
}

interface TaskForm {
  name: string
  enabled: boolean
  kind: AIAutomationKind
  prompt: string
  executionMode: AIAutomationExecutionMode
  intervalSeconds: string
  scheduleMode: 'interval' | 'fixed' | 'cron'
  fixedTime: string
  cronExpression: string
  cooldownSeconds: string
  targetKey: string
  value: string
  conditions: TaskConditionForm[]
  conditionMatch: AIAutomationConditionMatch
}

interface ConversationEntry {
  id: string
  role: 'user' | 'assistant'
  content: string
  run?: AIRun
}

interface TaskConditionForm {
  targetKey: string
  operator: AIAutomationConditionOperator
  value: string
}

const emptyTaskForm: TaskForm = { name: '', enabled: true, kind: 'schedule', prompt: '', executionMode: 'unattended', intervalSeconds: '300', scheduleMode: 'interval', fixedTime: '09:00', cronExpression: '0 9 * * *', cooldownSeconds: '60', targetKey: '', value: '', conditions: [], conditionMatch: 'all' }

const conditionOperatorLabels: Record<AIAutomationConditionOperator, string> = {
  equals: '等于', not_equals: '不等于', greater_than: '大于', greater_than_or_equal: '大于等于', less_than: '小于', less_than_or_equal: '小于等于',
}
const conditionMatchLabels: Record<AIAutomationConditionMatch, string> = { all: '全部满足', any: '满足任意一条' }

function numericType(type?: ValueType) { return type === 'int' || type === 'number' }
function operatorsFor(type?: ValueType): AIAutomationConditionOperator[] {
  return numericType(type) ? ['equals', 'not_equals', 'greater_than', 'greater_than_or_equal', 'less_than', 'less_than_or_equal'] : ['equals', 'not_equals']
}

function targetsOf(devices: Device[]): PropertyTarget[] {
  return devices.filter((device) => !device.removed).flatMap((device) => (device.endpoints ?? []).flatMap((endpoint) => (endpoint.capabilities ?? []).flatMap((capability) => (capability.properties ?? []).map((property) => ({
    key: [device.id, endpoint.id, capability.id, property.definition.id].join('\u0000'),
    deviceId: device.id,
    deviceName: device.name,
    endpointId: endpoint.id,
    capabilityId: capability.id,
    propertyId: property.definition.id,
    definition: property.definition,
    value: property.value,
  })))))
}

function valueText(value: PropertyValue): string {
  if (value.bool !== undefined) return String(value.bool)
  if (value.int !== undefined) return String(value.int)
  if (value.number !== undefined) return String(value.number)
  return value.string ?? ''
}

function typedValue(type: ValueType, raw: string): PropertyValue | null {
  if (type === 'bool') return raw === 'true' ? { type, bool: true } : raw === 'false' ? { type, bool: false } : null
  if (type === 'int') {
    const value = Number(raw)
    return Number.isInteger(value) ? { type, int: value } : null
  }
  if (type === 'number') {
    const value = Number(raw)
    return Number.isFinite(value) ? { type, number: value } : null
  }
  return { type, string: raw }
}

function fixedTimeCron(value: string): string {
  const match = value.match(/^(\d{2}):(\d{2})$/)
  if (!match) return ''
  const hour = Number(match[1]); const minute = Number(match[2])
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour > 23 || minute > 59) return ''
  return `${minute} ${hour} * * *`
}

function scheduleSummary(item: AIAutomation): string {
  if (item.kind !== 'schedule') return `状态触发 · 冷却 ${item.cooldownSeconds} 秒`
  const fixed = item.cronExpression?.match(/^(\d{1,2})\s+(\d{1,2})\s+\*\s+\*\s+\*$/)
  if (fixed) return `每天 ${fixed[2].padStart(2, '0')}:${fixed[1].padStart(2, '0')}（家庭时区）`
  if (item.cronExpression) return `Cron ${item.cronExpression}（家庭时区）`
  return `每 ${item.intervalSeconds} 秒`
}

function taskInput(item: AIAutomation): AIAutomationInput {
  return {
    name: item.name,
    enabled: item.enabled,
    kind: item.kind,
    prompt: item.prompt,
    executionMode: item.executionMode,
    intervalSeconds: item.intervalSeconds,
    cronExpression: item.cronExpression,
    cooldownSeconds: item.cooldownSeconds,
    trigger: item.trigger,
    conditions: item.conditions,
    conditionMatch: item.conditionMatch ?? 'all',
  }
}

function taskForm(item: AIAutomation, targets: PropertyTarget[]): TaskForm {
  const target = item.trigger && targets.find((value) => value.deviceId === item.trigger?.deviceId && value.endpointId === item.trigger?.endpointId && value.capabilityId === item.trigger?.capabilityId && value.propertyId === item.trigger?.propertyId)
  const fixed = item.cronExpression?.match(/^(\d{1,2})\s+(\d{1,2})\s+\*\s+\*\s+\*$/)
  return {
    name: item.name,
    enabled: item.enabled,
    kind: item.kind,
    prompt: item.prompt,
    executionMode: item.executionMode,
    intervalSeconds: String(item.intervalSeconds ?? 300),
    scheduleMode: item.cronExpression ? (fixed ? 'fixed' : 'cron') : 'interval',
    fixedTime: fixed ? `${fixed[2].padStart(2, '0')}:${fixed[1].padStart(2, '0')}` : '09:00',
    cronExpression: item.cronExpression ?? '0 9 * * *',
    cooldownSeconds: String(item.cooldownSeconds ?? 60),
    targetKey: target?.key ?? '',
    value: item.trigger ? valueText(item.trigger.value) : '',
    conditions: (item.conditions ?? []).map((condition) => {
      const target = targets.find((value) => value.deviceId === condition.deviceId && value.endpointId === condition.endpointId && value.capabilityId === condition.capabilityId && value.propertyId === condition.propertyId)
      return { targetKey: target?.key ?? '', operator: condition.operator, value: valueText(condition.value) }
    }),
    conditionMatch: item.conditionMatch ?? 'all',
  }
}

function runStatusLabel(status: AIRun['status'] | string): string {
  return status === 'awaiting_approval' ? '等待批准' : status === 'executed' ? '已执行' : status === 'completed' ? '已完成' : status === 'failed' ? '失败' : status || '未运行'
}

function RunAction({ action }: { action?: AIRunAction }) {
  if (!action) return null
  return <dl>
    <div><dt>设备</dt><dd>{action.deviceName || action.deviceId}</dd></div>
    <div><dt>属性</dt><dd>{action.propertyName || action.propertyId} → {valueText(action.value)}</dd></div>
    {action.usageNote && <div><dt>授权备注</dt><dd>{action.usageNote}</dd></div>}
  </dl>
}

function RunCard({ run, onApprove, approving }: { run: AIRun; onApprove: (id: string) => void; approving: boolean }) {
  return <article className={`ai-run is-${run.status}`} aria-label="AI 对话结果">
    <header><strong>AI 回复</strong><span>{runStatusLabel(run.status)}</span></header>
    <MarkdownMessage content={run.message || 'AI 未返回文字回复。'} />
    <RunAction action={run.action} />
    {run.status === 'awaiting_approval' && <div className="ai-run-actions"><button type="button" className="primary" onClick={() => onApprove(run.id)} disabled={approving}>{approving ? '批准中…' : '批准设备操作'}</button><small>批准前不会向设备发送写入命令。</small></div>}
  </article>
}

export function AIInteractionWorkspace({ devices }: { devices: Device[] }) {
  const targets = useMemo(() => targetsOf(devices), [devices])
  const [message, setMessage] = useState('')
  const [conversation, setConversation] = useState<ConversationEntry[]>(() => {
    try { return typeof sessionStorage === 'undefined' ? [] : JSON.parse(sessionStorage.getItem('homeloom.ai.conversation') ?? '[]') as ConversationEntry[] } catch { return [] }
  })
  const [taskRun, setTaskRun] = useState<AIRun | null>(null)
  const [running, setRunning] = useState(false)
  const [streamController, setStreamController] = useState<AbortController | null>(null)
  const [approving, setApproving] = useState(false)
  const [runTaskID, setRunTaskID] = useState<string | null>(null)
  const [automations, setAutomations] = useState<AIAutomation[]>([])
  const [loadingTasks, setLoadingTasks] = useState(true)
  const [savingTask, setSavingTask] = useState(false)
  const [editingID, setEditingID] = useState<string | null>(null)
  const [form, setForm] = useState<TaskForm>(emptyTaskForm)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    void listAIAutomations().then((items) => {
      if (active) { setAutomations(items); setError(null) }
    }).catch((cause) => {
      if (active) setError(cause instanceof Error ? cause.message : '读取 AI 自动任务失败')
    }).finally(() => { if (active) setLoadingTasks(false) })
    return () => { active = false }
  }, [])

  useEffect(() => {
    try { if (typeof sessionStorage !== 'undefined') sessionStorage.setItem('homeloom.ai.conversation', JSON.stringify(conversation.slice(-48))) } catch { /* browser storage is optional */ }
  }, [conversation])

  const selectedTarget = targets.find((item) => item.key === form.targetKey)

  function updateForm(change: Partial<TaskForm>) { setForm((value) => ({ ...value, ...change })) }

  function selectTarget(key: string) {
    const next = targets.find((item) => item.key === key)
    updateForm({ targetKey: key, value: next ? valueText(next.value) : '' })
  }

  function updateCondition(index: number, change: Partial<TaskConditionForm>) {
    setForm((value) => ({ ...value, conditions: value.conditions.map((condition, current) => current === index ? { ...condition, ...change } : condition) }))
  }

  function selectConditionTarget(index: number, key: string) {
    const next = targets.find((item) => item.key === key)
    const previous = form.conditions[index]
    const operator = next && !operatorsFor(next.definition.type).includes(previous.operator) ? 'equals' : previous.operator
    updateCondition(index, { targetKey: key, operator, value: next ? valueText(next.value) : '' })
  }

  async function sendMessage() {
    if (!message.trim()) { setError('请输入要交给 AI 的内容'); return }
    const content = message.trim()
    const id = `message-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
    const history: AIConversationTurn[] = conversation.slice(-24).map((entry) => ({ role: entry.role, content: entry.content })).filter((entry) => entry.content.trim() !== '')
    const controller = new AbortController()
    setConversation((entries) => [...entries, { id: `${id}-user`, role: 'user', content }, { id, role: 'assistant', content: '' }])
    setRunning(true); setError(null); setNotice(null)
    setStreamController(controller)
    try {
      await startAIRunStream(content, history, controller.signal, (event) => {
        if (event.type === 'delta' && event.delta) {
          setConversation((entries) => entries.map((entry) => entry.id === id ? { ...entry, content: entry.content + event.delta } : entry))
        }
        if (event.type === 'run' && event.run) {
          setConversation((entries) => entries.map((entry) => entry.id === id ? { ...entry, content: event.run?.message || entry.content || 'AI 未返回文字回复。', run: event.run } : entry))
        }
        if (event.type === 'error') throw new Error(event.error || 'AI 请求失败')
      })
      setMessage('')
    } catch (cause) {
      const cancelled = cause instanceof DOMException && cause.name === 'AbortError'
      setConversation((entries) => entries.map((entry) => entry.id === id && !entry.content ? { ...entry, content: cancelled ? '已取消本次 AI 请求。' : 'AI 请求未完成。' } : entry))
      if (!cancelled) setError(cause instanceof Error ? cause.message : 'AI 请求失败')
    } finally { setRunning(false); setStreamController(null) }
  }

  async function approve(id: string, taskID?: string) {
    setApproving(true); setError(null)
    try {
      if (taskID) {
        const result = await approveAIAutomationRun(taskID, id)
        setAutomations((items) => items.map((item) => item.id === result.automation.id ? result.automation : item))
        setTaskRun(result.run)
      } else {
        const updated = await approveAIRun(id)
        setConversation((entries) => entries.map((entry) => entry.run?.id === id ? { ...entry, content: updated.message, run: updated } : entry))
      }
    } catch (cause) { setError(cause instanceof Error ? cause.message : '批准设备操作失败') } finally { setApproving(false) }
  }

  function cancelMessage() {
    streamController?.abort()
  }

  function clearConversation() {
    if (running) return
    setConversation([])
    try { if (typeof sessionStorage !== 'undefined') sessionStorage.removeItem('homeloom.ai.conversation') } catch { /* browser storage is optional */ }
  }

  function inputFromForm(): AIAutomationInput | null {
    const interval = Number.parseInt(form.intervalSeconds, 10)
    const cooldown = Number.parseInt(form.cooldownSeconds, 10)
    if (!form.name.trim() || !form.prompt.trim()) { setError('请填写任务名称和任务提示词'); return null }
    const conditions: AIAutomationCondition[] = []
    for (const [index, condition] of form.conditions.entries()) {
      const target = targets.find((item) => item.key === condition.targetKey)
      if (!target) { setError(`请选择第 ${index + 1} 个判断条件的属性`); return null }
      if (!operatorsFor(target.definition.type).includes(condition.operator)) { setError(`第 ${index + 1} 个判断条件的运算符不适用于该属性`); return null }
      const value = typedValue(target.definition.type, condition.value)
      if (!value) { setError(`第 ${index + 1} 个判断条件的值与属性类型不匹配`); return null }
      conditions.push({ deviceId: target.deviceId, endpointId: target.endpointId, capabilityId: target.capabilityId, propertyId: target.propertyId, operator: condition.operator, value })
    }
    if (form.kind === 'schedule') {
      if (form.scheduleMode === 'interval') {
        if (!Number.isInteger(interval) || interval < 60) { setError('定时任务的间隔至少为 60 秒'); return null }
        return { name: form.name.trim(), enabled: form.enabled, kind: 'schedule', prompt: form.prompt.trim(), executionMode: form.executionMode, intervalSeconds: interval, conditions, conditionMatch: form.conditionMatch }
      }
      const cronExpression = form.scheduleMode === 'fixed' ? fixedTimeCron(form.fixedTime) : form.cronExpression.trim()
      if (!cronExpression) { setError('请填写固定执行时间或 Cron 表达式'); return null }
      return { name: form.name.trim(), enabled: form.enabled, kind: 'schedule', prompt: form.prompt.trim(), executionMode: form.executionMode, cronExpression, conditions, conditionMatch: form.conditionMatch }
    }
    if (!selectedTarget) { setError('请选择已授权给 AI 的状态属性'); return null }
    const value = typedValue(selectedTarget.definition.type, form.value)
    if (!value) { setError('触发值与属性类型不匹配'); return null }
    if (!Number.isInteger(cooldown) || cooldown < 60) { setError('触发冷却时间至少为 60 秒'); return null }
    return {
      name: form.name.trim(), enabled: form.enabled, kind: 'trigger', prompt: form.prompt.trim(), executionMode: form.executionMode, cooldownSeconds: cooldown,
      trigger: { deviceId: selectedTarget.deviceId, endpointId: selectedTarget.endpointId, capabilityId: selectedTarget.capabilityId, propertyId: selectedTarget.propertyId, value }, conditions, conditionMatch: form.conditionMatch,
    }
  }

  async function saveTask() {
    const input = inputFromForm()
    if (!input) return
    setSavingTask(true); setError(null); setNotice(null)
    try {
      const saved = editingID ? await saveAIAutomation(editingID, input) : await createAIAutomation(input)
      setAutomations((items) => editingID ? items.map((item) => item.id === saved.id ? saved : item) : [...items, saved])
      setEditingID(null); setForm(emptyTaskForm); setNotice('自动任务已保存。')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存 AI 自动任务失败')
    } finally { setSavingTask(false) }
  }

  function editTask(item: AIAutomation) { setEditingID(item.id); setForm(taskForm(item, targets)); setError(null); setNotice(null) }

  async function toggleTask(item: AIAutomation) {
    setError(null); setNotice(null)
    try {
      const saved = await saveAIAutomation(item.id, { ...taskInput(item), enabled: !item.enabled })
      setAutomations((items) => items.map((value) => value.id === saved.id ? saved : value))
    } catch (cause) { setError(cause instanceof Error ? cause.message : '更新 AI 自动任务失败') }
  }

  async function removeTask(item: AIAutomation) {
    setError(null); setNotice(null)
    try {
      await deleteAIAutomation(item.id)
      setAutomations((items) => items.filter((value) => value.id !== item.id))
      if (editingID === item.id) { setEditingID(null); setForm(emptyTaskForm) }
    } catch (cause) { setError(cause instanceof Error ? cause.message : '删除 AI 自动任务失败') }
  }

  async function runTask(item: AIAutomation) {
    setError(null); setNotice(null)
    try {
      const result = await runAIAutomation(item.id)
      setAutomations((items) => items.map((value) => value.id === result.automation.id ? result.automation : value))
      setTaskRun(result.run)
		setRunTaskID(item.id)
      setNotice(result.run.autoApproved ? '已手动启动自动任务；AI 生成的设备操作已按无人值守策略自动批准。' : '已手动启动自动任务；如生成设备操作，可在上方批准。')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '运行 AI 自动任务失败') }
  }

  return <section className="ai-interaction-workspace" aria-label="AI 对话与自动任务">
    <section className="ai-conversation" aria-labelledby="ai-conversation-heading">
      <div className="ai-panel-heading"><div><span>AI 对话</span><h3 id="ai-conversation-heading">向 AI 下达任务</h3></div><small>本页会保留当前浏览器会话的历史并作为上下文；涉及设备写入时，仍只会返回待批准计划。</small></div>
      <div className="ai-conversation-history" aria-label="AI 会话历史">{conversation.length === 0 ? <p>开始一段新对话吧。</p> : conversation.map((entry) => entry.role === 'user' ? <article key={entry.id} className="ai-message is-user"><strong>你</strong><p>{entry.content}</p></article> : <RunCard key={entry.id} run={entry.run ?? { id: entry.id, status: running ? 'completed' : 'failed', message: entry.content || 'AI 正在思考…', createdAt: '' }} onApprove={(id) => void approve(id)} approving={approving} />)}</div>
      <label className="ai-message-input">消息<textarea aria-label="向 AI 发送消息" value={message} onChange={(event) => setMessage(event.target.value)} maxLength={16384} placeholder="例如：检查客厅灯状态；如需打开，请先给我操作计划。" /></label>
      <div className="ai-conversation-actions"><button type="button" className="primary" onClick={() => void sendMessage()} disabled={running}>{running ? 'AI 思考中，请稍候…' : '发送给 AI'}</button>{running && <button type="button" className="danger" onClick={cancelMessage}>取消本次请求</button>}<button type="button" onClick={clearConversation} disabled={running || conversation.length === 0}>清空会话</button><small>复杂分析最多可能需要约 6 分钟；取消会中断本次模型请求，且不会自动执行设备操作。</small></div>
      {taskRun && <RunCard run={taskRun} onApprove={(id) => void approve(id, runTaskID ?? undefined)} approving={approving} />}
    </section>

    <section className="ai-automations" aria-labelledby="ai-automations-heading">
      <div className="ai-panel-heading"><div><span>AI 自动化</span><h3 id="ai-automations-heading">定时与状态触发任务</h3></div><small>每次运行都是独立 AI 会话；默认由任务授权 AI 自动批准其设备操作，并保存最近 50 条运行记录。</small></div>
      <div className="ai-task-form">
        <label>任务名称<input aria-label="自动任务名称" value={form.name} onChange={(event) => updateForm({ name: event.target.value })} maxLength={120} placeholder="例如：每日设备巡检" /></label>
        <label>任务类型<select aria-label="自动任务类型" value={form.kind} onChange={(event) => updateForm({ kind: event.target.value as AIAutomationKind })}><option value="schedule">定时任务</option><option value="trigger">状态触发任务</option></select></label>
        <label className="ai-task-enabled"><input aria-label="启用自动任务" type="checkbox" checked={form.enabled} onChange={(event) => updateForm({ enabled: event.target.checked })} />启用保存后的自动任务</label>
        <label className="ai-task-enabled"><input aria-label="无人值守执行" type="checkbox" checked={form.executionMode === 'unattended'} onChange={(event) => updateForm({ executionMode: event.target.checked ? 'unattended' : 'manual' })} />无人值守执行（AI 生成操作计划后自动批准）</label>
        <label className="ai-task-prompt">任务提示词<textarea aria-label="自动任务提示词" value={form.prompt} onChange={(event) => updateForm({ prompt: event.target.value })} maxLength={16384} placeholder="例如：检查已授权的设备状态，概述异常；若需要修复，只生成操作计划。" /></label>
        <section className="ai-task-conditions" aria-labelledby="ai-task-conditions-heading">
          <div><strong id="ai-task-conditions-heading">判断条件（可选）</strong><small>自动定时或状态触发时按所选关系评估；状态未知、不可用或过期不算满足。“立即运行”不受条件限制。</small></div>
          <label className="ai-task-condition-match">条件关系<select aria-label="判断条件关系" value={form.conditionMatch} onChange={(event) => updateForm({ conditionMatch: event.target.value as AIAutomationConditionMatch })} disabled={form.conditions.length < 2}><option value="all">全部满足</option><option value="any">满足任意一条</option></select><small>{form.conditions.length < 2 ? '添加至少两条条件后可选择关系。' : `自动执行时需${conditionMatchLabels[form.conditionMatch]}。`}</small></label>
          {form.conditions.map((condition, index) => {
            const target = targets.find((item) => item.key === condition.targetKey)
            return <div className="ai-task-condition" key={`${condition.targetKey}-${index}`}>
              <label>条件属性<select aria-label={`判断条件 ${index + 1} 属性`} value={condition.targetKey} onChange={(event) => selectConditionTarget(index, event.target.value)}><option value="">选择一个已授权给 AI 的属性</option>{targets.map((item) => <option key={item.key} value={item.key}>{item.deviceName} · {item.definition.name} ({item.definition.type})</option>)}</select></label>
              <label>比较<select aria-label={`判断条件 ${index + 1} 运算符`} value={condition.operator} onChange={(event) => updateCondition(index, { operator: event.target.value as AIAutomationConditionOperator })}>{operatorsFor(target?.definition.type).map((operator) => <option key={operator} value={operator}>{conditionOperatorLabels[operator]}</option>)}</select></label>
              <label>条件值{target?.definition.type === 'bool' ? <select aria-label={`判断条件 ${index + 1} 值`} value={condition.value} onChange={(event) => updateCondition(index, { value: event.target.value })}><option value="true">true</option><option value="false">false</option></select> : <input aria-label={`判断条件 ${index + 1} 值`} type={numericType(target?.definition.type) ? 'number' : 'text'} value={condition.value} onChange={(event) => updateCondition(index, { value: event.target.value })} placeholder={target ? target.definition.type : '先选择属性'} />}</label>
              <button type="button" className="danger" onClick={() => updateForm({ conditions: form.conditions.filter((_, current) => current !== index) })}>移除条件</button>
            </div>
          })}
          <button type="button" onClick={() => updateForm({ conditions: [...form.conditions, { targetKey: '', operator: 'equals', value: '' }] })} disabled={form.conditions.length >= 16}>添加判断条件</button>
        </section>
        {form.kind === 'schedule' ? <section className="ai-task-schedule" aria-label="定时计划"><label>执行方式<select aria-label="定时执行方式" value={form.scheduleMode} onChange={(event) => updateForm({ scheduleMode: event.target.value as TaskForm['scheduleMode'] })}><option value="interval">固定间隔</option><option value="fixed">每天固定时间</option><option value="cron">Cron 表达式</option></select></label>{form.scheduleMode === 'interval' ? <label>间隔（秒）<input aria-label="定时间隔秒数" type="number" min="60" max="604800" value={form.intervalSeconds} onChange={(event) => updateForm({ intervalSeconds: event.target.value })} /><small>最短 60 秒。</small></label> : form.scheduleMode === 'fixed' ? <label>每天执行时间<input aria-label="每天固定执行时间" type="time" value={form.fixedTime} onChange={(event) => updateForm({ fixedTime: event.target.value })} /><small>按 AI 设置中的家庭默认时区执行。</small></label> : <label>Cron 表达式<input aria-label="Cron 表达式" value={form.cronExpression} onChange={(event) => updateForm({ cronExpression: event.target.value })} placeholder="0 9 * * *" /><small>五字段：分 时 日 月 周；按家庭默认时区执行。</small></label>}</section> : <>
          <label className="ai-trigger-property">触发属性<select aria-label="状态触发属性" value={form.targetKey} onChange={(event) => selectTarget(event.target.value)}><option value="">选择一个已授权给 AI 的属性</option>{targets.map((target) => <option key={target.key} value={target.key}>{target.deviceName} · {target.definition.name} ({target.definition.type})</option>)}</select><small>保存时会再次验证该属性的 AI 授权。</small></label>
          <label>触发值{selectedTarget?.definition.type === 'bool' ? <select aria-label="状态触发值" value={form.value} onChange={(event) => updateForm({ value: event.target.value })}><option value="true">true</option><option value="false">false</option></select> : <input aria-label="状态触发值" type={selectedTarget?.definition.type === 'int' || selectedTarget?.definition.type === 'number' ? 'number' : 'text'} value={form.value} onChange={(event) => updateForm({ value: event.target.value })} placeholder={selectedTarget ? selectedTarget.definition.type : '先选择属性'} />}</label>
          <label>冷却（秒）<input aria-label="触发冷却秒数" type="number" min="60" max="604800" value={form.cooldownSeconds} onChange={(event) => updateForm({ cooldownSeconds: event.target.value })} /><small>同一条件至少间隔 60 秒才会再运行。</small></label>
        </>}
        <div className="ai-task-form-actions"><button type="button" className="primary" onClick={() => void saveTask()} disabled={savingTask}>{savingTask ? '保存中…' : editingID ? '更新自动任务' : '保存自动任务'}</button>{editingID && <button type="button" onClick={() => { setEditingID(null); setForm(emptyTaskForm) }}>取消编辑</button>}</div>
      </div>
      {loadingTasks ? <p role="status">正在读取自动任务…</p> : <div className="ai-task-list">{automations.map((item) => <article key={item.id}>
        <header><div><strong>{item.name}</strong><small>{scheduleSummary(item)} · {item.executionMode === 'unattended' ? '无人值守' : '人工批准'}</small></div><span className={item.enabled ? 'is-enabled' : ''}>{item.enabled ? '已启用' : '已暂停'}</span></header>
        <p>{item.prompt}</p>
        {item.conditions && item.conditions.length > 0 && <small className="ai-task-conditions-summary">判断条件：{item.conditions.map((condition) => `${targets.find((target) => target.deviceId === condition.deviceId && target.endpointId === condition.endpointId && target.capabilityId === condition.capabilityId && target.propertyId === condition.propertyId)?.definition.name ?? condition.propertyId} ${conditionOperatorLabels[condition.operator]} ${valueText(condition.value)}`).join('；')}（{conditionMatchLabels[item.conditionMatch ?? 'all']}）</small>}
        {item.lastRunStatus && <small className="ai-task-last-run">最近执行：{runStatusLabel(item.lastRunStatus)}{item.lastRunMessage ? ` · ${item.lastRunMessage}` : ''}</small>}
        {item.runHistory && item.runHistory.length > 0 && <details className="ai-task-history"><summary>运行记录（保留最近 {item.runHistory.length} / 50 条）</summary><div>{item.runHistory.map((record) => <article key={`${record.id}-${record.createdAt}`}>
          <header><strong>{record.source === 'schedule' ? '定时运行' : record.source === 'trigger' ? '状态触发' : '手动运行'}</strong><small>{record.createdAt ? new Date(record.createdAt).toLocaleString('zh-CN', { hour12: false }) : '刚刚'} · {runStatusLabel(record.status)}{record.autoApproved ? ' · AI 自动批准' : ''}</small></header>
          <MarkdownMessage content={record.message || 'AI 未返回文字回复。'} />
          <RunAction action={record.action} />
        </article>)}</div></details>}
        <div className="ai-task-actions"><button type="button" onClick={() => void runTask(item)} disabled={!item.enabled}>立即运行</button><button type="button" onClick={() => void toggleTask(item)}>{item.enabled ? '暂停' : '启用'}</button><button type="button" onClick={() => editTask(item)}>编辑</button><button type="button" className="danger" onClick={() => void removeTask(item)}>删除</button></div>
      </article>)}{automations.length === 0 && <p className="ai-automation-empty">尚未配置自动任务。</p>}</div>}
    </section>
    {error && <small className="field-error" role="alert">{error}</small>}{notice && <small className="maintenance-message" role="status">{notice}</small>}
  </section>
}
