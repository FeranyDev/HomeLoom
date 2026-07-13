import { useEffect, useState } from 'react'
import type { AuditEvent, DeviceCommand, Diagnostics, RuntimeSettings } from '../types/diagnostics'

function expectedValue(command: DeviceCommand): string { if (command.kind === 'action') return Object.entries(command.parameters ?? {}).map(([key, value]) => `${key}=${value.bool ?? value.int ?? value.number ?? value.string ?? '—'}`).join(', ') || '无参数'; const value = command.expected; if (!value) return '—'; if (value.bool !== undefined) return String(value.bool); if (value.int !== undefined) return String(value.int); if (value.number !== undefined) return String(value.number); return value.string ?? '—' }
function megabytes(bytes: number): string { return `${(bytes / 1024 / 1024).toFixed(1)}MB` }

function RuntimeSettingsCard({ settings, onSave }: { settings: RuntimeSettings | null; onSave: (settings: RuntimeSettings) => Promise<void> }) {
  const [seconds, setSeconds] = useState(settings?.commandTimeoutSeconds ?? 5)
  const [historyLimit, setHistoryLimit] = useState(settings?.commandHistoryLimit ?? 1000)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { if (settings) { setSeconds(settings.commandTimeoutSeconds); setHistoryLimit(settings.commandHistoryLimit) } }, [settings])
  const save = async () => { if (seconds < 1 || seconds > 300) { setError('超时请输入 1–300 秒'); return }; if (historyLimit < 100 || historyLimit > 10000) { setError('历史上限请输入 100–10000'); return }; setSaving(true); setError(null); try { await onSave({ commandTimeoutSeconds: seconds, commandHistoryLimit: historyLimit }) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败') } finally { setSaving(false) } }
  return <div className="queue-card settings-card"><div><span>命令运行时设置</span><strong>{settings?.commandTimeoutSeconds ?? seconds}s / {settings?.commandHistoryLimit ?? historyLimit} 条</strong></div><label>确认超时秒数<input aria-label="命令确认超时秒数" type="number" min="1" max="300" value={seconds} onChange={(event) => setSeconds(Number(event.target.value))} /></label><label>历史保留上限<input aria-label="命令历史保留上限" type="number" min="100" max="10000" value={historyLimit} onChange={(event) => setHistoryLimit(Number(event.target.value))} /></label><button disabled={saving || !settings} onClick={() => void save()}>{saving ? '保存中…' : '保存并实时应用'}</button>{error && <small className="field-error" role="alert">{error}</small>}<small>存储于 SQLite。超时仅影响之后创建的命令；降低历史上限会立即清理最旧的终态记录，不删除执行中的命令。</small></div>
}

export function SystemDashboard({ diagnostics, commands, auditEvents = [], settings, onSettingsSave }: { diagnostics: Diagnostics | null; commands: DeviceCommand[]; auditEvents?: AuditEvent[]; settings: RuntimeSettings | null; onSettingsSave: (settings: RuntimeSettings) => Promise<void> }) {
	const [commandQuery, setCommandQuery] = useState('')
	const [commandStatus, setCommandStatus] = useState('all')
	const [auditQuery, setAuditQuery] = useState('')
  if (!diagnostics) return <div className="empty-state" role="status">正在加载诊断数据…</div>
	const normalizedQuery = commandQuery.trim().toLowerCase()
	const filteredCommands = commands.filter((command) => {
		const matchesStatus = commandStatus === 'all' || command.status === commandStatus || command.outcome === commandStatus
		const searchable = `${command.id} ${command.correlationId ?? ''} ${command.deviceId} ${command.endpointId} ${command.capabilityId} ${command.propertyId ?? ''} ${command.commandId ?? ''} ${command.error ?? ''}`.toLowerCase()
		return matchesStatus && (!normalizedQuery || searchable.includes(normalizedQuery))
	})
	const normalizedAuditQuery = auditQuery.trim().toLowerCase()
	const filteredAuditEvents = auditEvents.filter((event) => !normalizedAuditQuery || `${event.correlationId} ${event.action} ${event.resourceType} ${event.resourceId ?? ''} ${event.method} ${event.route} ${event.outcome}`.toLowerCase().includes(normalizedAuditQuery))
  const successRate = diagnostics.commandsStarted ? Math.round(diagnostics.commandsConfirmed / diagnostics.commandsStarted * 100) : 100
  const queueRate = diagnostics.eventQueueCapacity ? Math.round(diagnostics.eventQueuePending / diagnostics.eventQueueCapacity * 100) : 0
  const metrics = [
    ['在线设备', diagnostics.onlineDevices], ['暂时离线', diagnostics.offlineDevices], ['可用性未知', diagnostics.unknownDevices], ['人工禁用', diagnostics.disabledDevices ?? 0], ['来源已删除', diagnostics.removedDevices ?? 0], ['运行中 Provider', diagnostics.providersRunning], ['实时订阅', diagnostics.deviceSubscribers],
    ['已接收事件', diagnostics.eventsReceived], ['丢弃事件', diagnostics.eventsDropped + diagnostics.targetEventsDropped + diagnostics.stateEventsDropped], ['命令成功率', `${successRate}%`], ['过期状态', diagnostics.statesMarkedStale],
	['平均命令耗时', `${diagnostics.commandAverageLatencyMs.toFixed(1)}ms`],
	['命令排队中', diagnostics.commandQueuePending],
	['命令最大排队', diagnostics.commandQueueMaxPending],
	['Provider 重试', diagnostics.providerRetries],
	['被替代命令', diagnostics.commandsSuperseded],
	['合并重复写入', diagnostics.commandsCoalesced ?? 0],
	['结果未知命令', diagnostics.commandsOutcomeUnknown],
	['HomeKit 推送', diagnostics.homeKitPushes],
	['Goroutine', diagnostics.goroutines],
	['Go Heap', megabytes(diagnostics.heapAllocBytes)],
	['Heap 对象', diagnostics.heapObjects],
	['事件平均延迟', `${diagnostics.eventAverageLatencyMs.toFixed(1)}ms`],
	['事件最大延迟', `${diagnostics.eventMaxLatencyMs.toFixed(1)}ms`],
	['慢 Handler', diagnostics.slowEventHandlers],
	['SQLite 操作', diagnostics.databaseOperations],
	['SQLite 平均延迟', `${diagnostics.databaseAverageLatencyMs.toFixed(1)}ms`],
	['SQLite 最大延迟', `${diagnostics.databaseMaxLatencyMs.toFixed(1)}ms`],
	['Provider 时钟漂移', diagnostics.providerClockSkewEvents],
	['忽略乱序/重复事件', diagnostics.providerEventsIgnored ?? 0],
	['最大时钟偏差', `${diagnostics.providerMaxClockSkewMs.toFixed(0)}ms`],
  ]
  return <section className="system-dashboard"><div className="metric-grid">{metrics.map(([label, value]) => <article key={label}><span>{label}</span><strong>{value}</strong></article>)}</div>
    <div className="queue-card"><div><span>事件队列</span><strong>{diagnostics.eventQueuePending} / {diagnostics.eventQueueCapacity}</strong></div><div className="queue-track"><span style={{ width: `${Math.min(queueRate, 100)}%` }} /></div><small>当前占用 {queueRate}% · 核心队列满时会丢弃并计数，不阻塞 Provider 线程。</small></div>
    <RuntimeSettingsCard settings={settings} onSave={onSettingsSave} />
	<div className="artifact-card queue-card"><div><span>支持资料</span><strong>已自动脱敏</strong></div><p>配置导出不包含桥 PIN、Setup URI 或本地存储路径；Provider 凭据会替换为星号。诊断包额外包含版本、运行指标和最近审计记录。</p><div className="artifact-actions"><a href="/api/v1/system/config-export" download>导出脱敏配置</a><a href="/api/v1/system/diagnostic-bundle" download>下载诊断包</a></div><small>下载响应禁止浏览器缓存，分享前仍建议检查设备名称等非凭据数据。</small></div>
	<div className="audit-section command-section"><div className="command-heading"><h3>实时审计日志</h3><span>SQLite 持久化最近 5000 条 · 页面显示最近 200 条</span></div><div className="command-filters"><input aria-label="搜索审计日志" value={auditQuery} onChange={(event) => setAuditQuery(event.target.value)} placeholder="搜索资源、动作、路由或 correlation ID" /><span>{filteredAuditEvents.length} / {auditEvents.length}</span></div>{auditEvents.length === 0 ? <div className="command-empty">还没有管理操作记录</div> : filteredAuditEvents.length === 0 ? <div className="command-empty">没有匹配的审计记录</div> : <div className="audit-table"><div className="audit-row command-header"><span>资源 / 动作</span><span>结果</span><span>Correlation ID</span><span>时间</span></div>{filteredAuditEvents.map((event) => <div className="audit-row" key={event.id}><span><b>{event.resourceType}{event.resourceId ? ` · ${event.resourceId}` : ''}</b><small>{event.method} {event.route} · {event.action}</small></span><span><i className={`command-status is-${event.outcome === 'succeeded' ? 'confirmed' : 'rejected'}`}>{event.status}</i><small>{event.outcome}</small></span><code title={event.correlationId}>{event.correlationId}</code><time>{new Date(event.createdAt).toLocaleString()}</time></div>)}</div>}</div>
    <div className="command-section"><div className="command-heading"><h3>命令历史</h3><span>内存中最多保留 {settings?.commandHistoryLimit ?? 1000} 条记录</span></div><div className="command-filters"><input aria-label="搜索命令" value={commandQuery} onChange={(event) => setCommandQuery(event.target.value)} placeholder="搜索设备、属性、动作、错误或命令 ID" /><select aria-label="命令状态" value={commandStatus} onChange={(event) => setCommandStatus(event.target.value)}><option value="all">全部状态</option><option value="queued">queued</option><option value="sent">sent</option><option value="accepted">accepted</option><option value="confirmed">confirmed</option><option value="rejected">rejected</option><option value="timeout">timeout</option><option value="superseded">superseded</option><option value="unknown">outcome unknown</option></select><span>{filteredCommands.length} / {commands.length}</span></div>{commands.length === 0 ? <div className="command-empty">还没有控制命令</div> : filteredCommands.length === 0 ? <div className="command-empty">没有匹配的命令</div> : <div className="command-table"><div className="command-row command-header"><span>设备 / 属性或动作</span><span>期望值 / 参数</span><span>状态 / 结果</span><span>更新时间</span></div>{filteredCommands.map((command) => <div className="command-row" key={command.id}><span><b>{command.deviceId}</b><small>{command.capabilityId}.{command.commandId ?? command.propertyId}</small><small>{command.id}</small>{command.correlationId && <small>trace: {command.correlationId}</small>}{Boolean(command.coalescedRequests) && <small>合并重复请求 × {command.coalescedRequests}</small>}</span><code>{expectedValue(command)}</code><span><i className={`command-status is-${command.status}`}>{command.status}</i>{command.outcome && <small>outcome: {command.outcome}</small>}{command.error && <small>{command.error}</small>}</span><time>{new Date(command.updatedAt).toLocaleString()}</time></div>)}</div>}</div>
  </section>
}
