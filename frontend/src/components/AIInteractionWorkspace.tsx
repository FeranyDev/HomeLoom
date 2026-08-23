import { useEffect, useMemo, useState } from 'react'
import { approveAIAutomationRun, approveAIRun, createAIAutomation, deleteAIAutomation, listAIAutomations, runAIAutomation, saveAIAutomation, startAIRun } from '../api/ai'
import type { AIAutomation, AIAutomationExecutionMode, AIAutomationInput, AIAutomationKind, AIRun, AIRunAction } from '../types/ai'
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
  cooldownSeconds: string
  targetKey: string
  value: string
}

const emptyTaskForm: TaskForm = { name: '', enabled: true, kind: 'schedule', prompt: '', executionMode: 'unattended', intervalSeconds: '300', cooldownSeconds: '60', targetKey: '', value: '' }

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

function taskInput(item: AIAutomation): AIAutomationInput {
  return {
    name: item.name,
    enabled: item.enabled,
    kind: item.kind,
    prompt: item.prompt,
    executionMode: item.executionMode,
    intervalSeconds: item.intervalSeconds,
    cooldownSeconds: item.cooldownSeconds,
    trigger: item.trigger,
  }
}

function taskForm(item: AIAutomation, targets: PropertyTarget[]): TaskForm {
  const target = item.trigger && targets.find((value) => value.deviceId === item.trigger?.deviceId && value.endpointId === item.trigger?.endpointId && value.capabilityId === item.trigger?.capabilityId && value.propertyId === item.trigger?.propertyId)
  return {
    name: item.name,
    enabled: item.enabled,
    kind: item.kind,
    prompt: item.prompt,
    executionMode: item.executionMode,
    intervalSeconds: String(item.intervalSeconds ?? 300),
    cooldownSeconds: String(item.cooldownSeconds ?? 60),
    targetKey: target?.key ?? '',
    value: item.trigger ? valueText(item.trigger.value) : '',
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
  const [run, setRun] = useState<AIRun | null>(null)
  const [running, setRunning] = useState(false)
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

  const selectedTarget = targets.find((item) => item.key === form.targetKey)

  function updateForm(change: Partial<TaskForm>) { setForm((value) => ({ ...value, ...change })) }

  function selectTarget(key: string) {
    const next = targets.find((item) => item.key === key)
    updateForm({ targetKey: key, value: next ? valueText(next.value) : '' })
  }

  async function sendMessage() {
    if (!message.trim()) { setError('请输入要交给 AI 的内容'); return }
    setRunning(true); setError(null); setNotice(null)
    try {
      setRun(await startAIRun(message.trim()))
      setRunTaskID(null)
      setMessage('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'AI 请求失败')
    } finally { setRunning(false) }
  }

  async function approve(id: string) {
    setApproving(true); setError(null)
    try {
      if (runTaskID) {
        const result = await approveAIAutomationRun(runTaskID, id)
        setAutomations((items) => items.map((item) => item.id === result.automation.id ? result.automation : item))
        setRun(result.run)
      } else {
        setRun(await approveAIRun(id))
      }
    } catch (cause) { setError(cause instanceof Error ? cause.message : '批准设备操作失败') } finally { setApproving(false) }
  }

  function inputFromForm(): AIAutomationInput | null {
    const interval = Number.parseInt(form.intervalSeconds, 10)
    const cooldown = Number.parseInt(form.cooldownSeconds, 10)
    if (!form.name.trim() || !form.prompt.trim()) { setError('请填写任务名称和任务提示词'); return null }
    if (form.kind === 'schedule') {
      if (!Number.isInteger(interval) || interval < 60) { setError('定时任务的间隔至少为 60 秒'); return null }
      return { name: form.name.trim(), enabled: form.enabled, kind: 'schedule', prompt: form.prompt.trim(), executionMode: form.executionMode, intervalSeconds: interval }
    }
    if (!selectedTarget) { setError('请选择已授权给 AI 的状态属性'); return null }
    const value = typedValue(selectedTarget.definition.type, form.value)
    if (!value) { setError('触发值与属性类型不匹配'); return null }
    if (!Number.isInteger(cooldown) || cooldown < 60) { setError('触发冷却时间至少为 60 秒'); return null }
    return {
      name: form.name.trim(), enabled: form.enabled, kind: 'trigger', prompt: form.prompt.trim(), executionMode: form.executionMode, cooldownSeconds: cooldown,
      trigger: { deviceId: selectedTarget.deviceId, endpointId: selectedTarget.endpointId, capabilityId: selectedTarget.capabilityId, propertyId: selectedTarget.propertyId, value },
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
      setRun(result.run)
      setRunTaskID(item.id)
      setNotice(result.run.autoApproved ? '已手动启动自动任务；AI 生成的设备操作已按无人值守策略自动批准。' : '已手动启动自动任务；如生成设备操作，可在上方批准。')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '运行 AI 自动任务失败') }
  }

  return <section className="ai-interaction-workspace" aria-label="AI 对话与自动任务">
    <section className="ai-conversation" aria-labelledby="ai-conversation-heading">
      <div className="ai-panel-heading"><div><span>AI 对话</span><h3 id="ai-conversation-heading">向 AI 下达任务</h3></div><small>AI 仅能使用已授权设备和属性；涉及设备写入时，会先返回待批准计划。</small></div>
      <label className="ai-message-input">消息<textarea aria-label="向 AI 发送消息" value={message} onChange={(event) => setMessage(event.target.value)} maxLength={16384} placeholder="例如：检查客厅灯状态；如需打开，请先给我操作计划。" /></label>
      <div className="ai-conversation-actions"><button type="button" className="primary" onClick={() => void sendMessage()} disabled={running}>{running ? 'AI 思考中，请稍候…' : '发送给 AI'}</button><small>复杂分析最多可能需要约 6 分钟；请勿重复提交，不会自动执行设备操作。</small></div>
      {run && <RunCard run={run} onApprove={(id) => void approve(id)} approving={approving} />}
    </section>

    <section className="ai-automations" aria-labelledby="ai-automations-heading">
      <div className="ai-panel-heading"><div><span>AI 自动化</span><h3 id="ai-automations-heading">定时与状态触发任务</h3></div><small>每次运行都是独立 AI 会话；默认由任务授权 AI 自动批准其设备操作，并保存最近 50 条运行记录。</small></div>
      <div className="ai-task-form">
        <label>任务名称<input aria-label="自动任务名称" value={form.name} onChange={(event) => updateForm({ name: event.target.value })} maxLength={120} placeholder="例如：每日设备巡检" /></label>
        <label>任务类型<select aria-label="自动任务类型" value={form.kind} onChange={(event) => updateForm({ kind: event.target.value as AIAutomationKind })}><option value="schedule">定时任务</option><option value="trigger">状态触发任务</option></select></label>
        <label className="ai-task-enabled"><input aria-label="启用自动任务" type="checkbox" checked={form.enabled} onChange={(event) => updateForm({ enabled: event.target.checked })} />启用保存后的自动任务</label>
        <label className="ai-task-enabled"><input aria-label="无人值守执行" type="checkbox" checked={form.executionMode === 'unattended'} onChange={(event) => updateForm({ executionMode: event.target.checked ? 'unattended' : 'manual' })} />无人值守执行（AI 生成操作计划后自动批准）</label>
        <label className="ai-task-prompt">任务提示词<textarea aria-label="自动任务提示词" value={form.prompt} onChange={(event) => updateForm({ prompt: event.target.value })} maxLength={16384} placeholder="例如：检查已授权的设备状态，概述异常；若需要修复，只生成操作计划。" /></label>
        {form.kind === 'schedule' ? <label>间隔（秒）<input aria-label="定时间隔秒数" type="number" min="60" max="604800" value={form.intervalSeconds} onChange={(event) => updateForm({ intervalSeconds: event.target.value })} /><small>最短 60 秒。</small></label> : <>
          <label className="ai-trigger-property">触发属性<select aria-label="状态触发属性" value={form.targetKey} onChange={(event) => selectTarget(event.target.value)}><option value="">选择一个已授权给 AI 的属性</option>{targets.map((target) => <option key={target.key} value={target.key}>{target.deviceName} · {target.definition.name} ({target.definition.type})</option>)}</select><small>保存时会再次验证该属性的 AI 授权。</small></label>
          <label>触发值{selectedTarget?.definition.type === 'bool' ? <select aria-label="状态触发值" value={form.value} onChange={(event) => updateForm({ value: event.target.value })}><option value="true">true</option><option value="false">false</option></select> : <input aria-label="状态触发值" type={selectedTarget?.definition.type === 'int' || selectedTarget?.definition.type === 'number' ? 'number' : 'text'} value={form.value} onChange={(event) => updateForm({ value: event.target.value })} placeholder={selectedTarget ? selectedTarget.definition.type : '先选择属性'} />}</label>
          <label>冷却（秒）<input aria-label="触发冷却秒数" type="number" min="60" max="604800" value={form.cooldownSeconds} onChange={(event) => updateForm({ cooldownSeconds: event.target.value })} /><small>同一条件至少间隔 60 秒才会再运行。</small></label>
        </>}
        <div className="ai-task-form-actions"><button type="button" className="primary" onClick={() => void saveTask()} disabled={savingTask}>{savingTask ? '保存中…' : editingID ? '更新自动任务' : '保存自动任务'}</button>{editingID && <button type="button" onClick={() => { setEditingID(null); setForm(emptyTaskForm) }}>取消编辑</button>}</div>
      </div>
      {loadingTasks ? <p role="status">正在读取自动任务…</p> : <div className="ai-task-list">{automations.map((item) => <article key={item.id}>
        <header><div><strong>{item.name}</strong><small>{item.kind === 'schedule' ? `每 ${item.intervalSeconds} 秒` : `状态触发 · 冷却 ${item.cooldownSeconds} 秒`} · {item.executionMode === 'unattended' ? '无人值守' : '人工批准'}</small></div><span className={item.enabled ? 'is-enabled' : ''}>{item.enabled ? '已启用' : '已暂停'}</span></header>
        <p>{item.prompt}</p>
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
