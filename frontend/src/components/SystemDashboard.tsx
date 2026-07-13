import { useEffect, useState } from 'react'
import type { DeviceCommand, Diagnostics, RuntimeSettings } from '../types/diagnostics'

function expectedValue(command: DeviceCommand): string { if (command.kind === 'action') return Object.entries(command.parameters ?? {}).map(([key, value]) => `${key}=${value.bool ?? value.number ?? value.string ?? '—'}`).join(', ') || '无参数'; const value = command.expected; if (!value) return '—'; if (value.bool !== undefined) return String(value.bool); if (value.number !== undefined) return String(value.number); return value.string ?? '—' }
function megabytes(bytes: number): string { return `${(bytes / 1024 / 1024).toFixed(1)}MB` }

function RuntimeSettingsCard({ settings, onSave }: { settings: RuntimeSettings | null; onSave: (settings: RuntimeSettings) => Promise<void> }) {
  const [seconds, setSeconds] = useState(settings?.commandTimeoutSeconds ?? 5)
  const [historyLimit, setHistoryLimit] = useState(settings?.commandHistoryLimit ?? 1000)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { if (settings) { setSeconds(settings.commandTimeoutSeconds); setHistoryLimit(settings.commandHistoryLimit) } }, [settings])
  const save = async () => { if (seconds < 1 || seconds > 300) { setError('超时请输入 1–300 秒'); return }; if (historyLimit < 100 || historyLimit > 10000) { setError('历史上限请输入 100–10000'); return }; setSaving(true); setError(null); try { await onSave({ commandTimeoutSeconds: seconds, commandHistoryLimit: historyLimit }) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败') } finally { setSaving(false) } }
  return <div className="queue-card settings-card"><div><span>命令运行时设置</span><strong>{settings?.commandTimeoutSeconds ?? seconds}s / {settings?.commandHistoryLimit ?? historyLimit} 条</strong></div><label>确认超时秒数<input aria-label="命令确认超时秒数" type="number" min="1" max="300" value={seconds} onChange={(event) => setSeconds(Number(event.target.value))} /></label><label>历史保留上限<input aria-label="命令历史保留上限" type="number" min="100" max="10000" value={historyLimit} onChange={(event) => setHistoryLimit(Number(event.target.value))} /></label><button disabled={saving || !settings} onClick={() => void save()}>{saving ? '保存中…' : '保存并实时应用'}</button>{error && <small className="field-error">{error}</small>}<small>存储于 SQLite。超时仅影响之后创建的命令；降低历史上限会立即清理最旧的终态记录，不删除执行中的命令。</small></div>
}

export function SystemDashboard({ diagnostics, commands, settings, onSettingsSave }: { diagnostics: Diagnostics | null; commands: DeviceCommand[]; settings: RuntimeSettings | null; onSettingsSave: (settings: RuntimeSettings) => Promise<void> }) {
  if (!diagnostics) return <div className="empty-state">正在加载诊断数据…</div>
  const successRate = diagnostics.commandsStarted ? Math.round(diagnostics.commandsConfirmed / diagnostics.commandsStarted * 100) : 100
  const queueRate = diagnostics.eventQueueCapacity ? Math.round(diagnostics.eventQueuePending / diagnostics.eventQueueCapacity * 100) : 0
  const metrics = [
    ['在线设备', diagnostics.onlineDevices], ['离线设备', diagnostics.offlineDevices], ['可用性未知', diagnostics.unknownDevices], ['运行中 Provider', diagnostics.providersRunning], ['实时订阅', diagnostics.deviceSubscribers],
    ['已接收事件', diagnostics.eventsReceived], ['丢弃事件', diagnostics.eventsDropped + diagnostics.targetEventsDropped + diagnostics.stateEventsDropped], ['命令成功率', `${successRate}%`], ['过期状态', diagnostics.statesMarkedStale],
	['平均命令耗时', `${diagnostics.commandAverageLatencyMs.toFixed(1)}ms`],
	['命令排队中', diagnostics.commandQueuePending],
	['命令最大排队', diagnostics.commandQueueMaxPending],
	['Provider 重试', diagnostics.providerRetries],
	['被替代命令', diagnostics.commandsSuperseded],
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
	['最大时钟偏差', `${diagnostics.providerMaxClockSkewMs.toFixed(0)}ms`],
  ]
  return <section className="system-dashboard"><div className="metric-grid">{metrics.map(([label, value]) => <article key={label}><span>{label}</span><strong>{value}</strong></article>)}</div>
    <div className="queue-card"><div><span>事件队列</span><strong>{diagnostics.eventQueuePending} / {diagnostics.eventQueueCapacity}</strong></div><div className="queue-track"><span style={{ width: `${Math.min(queueRate, 100)}%` }} /></div><small>当前占用 {queueRate}% · 核心队列满时会丢弃并计数，不阻塞 Provider 线程。</small></div>
    <RuntimeSettingsCard settings={settings} onSave={onSettingsSave} />
    <div className="command-section"><div className="command-heading"><h3>命令历史</h3><span>内存中最多保留 {settings?.commandHistoryLimit ?? 1000} 条终态记录</span></div>{commands.length === 0 ? <div className="command-empty">还没有控制命令</div> : <div className="command-table"><div className="command-row command-header"><span>设备 / 属性或动作</span><span>期望值 / 参数</span><span>状态 / 结果</span><span>更新时间</span></div>{commands.map((command) => <div className="command-row" key={command.id}><span><b>{command.deviceId}</b><small>{command.capabilityId}.{command.commandId ?? command.propertyId}</small></span><code>{expectedValue(command)}</code><span><i className={`command-status is-${command.status}`}>{command.status}</i>{command.outcome && <small>outcome: {command.outcome}</small>}{command.error && <small>{command.error}</small>}</span><time>{new Date(command.updatedAt).toLocaleString()}</time></div>)}</div>}</div>
  </section>
}
