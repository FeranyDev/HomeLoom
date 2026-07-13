import type { DeviceCommand, Diagnostics } from '../types/diagnostics'

function expectedValue(command: DeviceCommand): string { if (command.kind === 'action') return Object.entries(command.parameters ?? {}).map(([key, value]) => `${key}=${value.bool ?? value.number ?? value.string ?? '—'}`).join(', ') || '无参数'; const value = command.expected; if (!value) return '—'; if (value.bool !== undefined) return String(value.bool); if (value.number !== undefined) return String(value.number); return value.string ?? '—' }
function megabytes(bytes: number): string { return `${(bytes / 1024 / 1024).toFixed(1)}MB` }

export function SystemDashboard({ diagnostics, commands }: { diagnostics: Diagnostics | null; commands: DeviceCommand[] }) {
  if (!diagnostics) return <div className="empty-state">正在加载诊断数据…</div>
  const successRate = diagnostics.commandsStarted ? Math.round(diagnostics.commandsConfirmed / diagnostics.commandsStarted * 100) : 100
  const queueRate = diagnostics.eventQueueCapacity ? Math.round(diagnostics.eventQueuePending / diagnostics.eventQueueCapacity * 100) : 0
  const metrics = [
    ['在线设备', diagnostics.onlineDevices], ['离线设备', diagnostics.offlineDevices], ['运行中 Provider', diagnostics.providersRunning], ['实时订阅', diagnostics.deviceSubscribers],
    ['已接收事件', diagnostics.eventsReceived], ['丢弃事件', diagnostics.eventsDropped + diagnostics.targetEventsDropped + diagnostics.stateEventsDropped], ['命令成功率', `${successRate}%`], ['过期状态', diagnostics.statesMarkedStale],
	['平均命令耗时', `${diagnostics.commandAverageLatencyMs.toFixed(1)}ms`],
	['Provider 重试', diagnostics.providerRetries],
	['被替代命令', diagnostics.commandsSuperseded],
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
  ]
  return <section className="system-dashboard"><div className="metric-grid">{metrics.map(([label, value]) => <article key={label}><span>{label}</span><strong>{value}</strong></article>)}</div>
    <div className="queue-card"><div><span>事件队列</span><strong>{diagnostics.eventQueuePending} / {diagnostics.eventQueueCapacity}</strong></div><div className="queue-track"><span style={{ width: `${Math.min(queueRate, 100)}%` }} /></div><small>当前占用 {queueRate}% · 核心队列满时会丢弃并计数，不阻塞 Provider 线程。</small></div>
    <div className="command-section"><div className="command-heading"><h3>命令历史</h3><span>内存中最多保留 1000 条终态记录</span></div>{commands.length === 0 ? <div className="command-empty">还没有控制命令</div> : <div className="command-table"><div className="command-row command-header"><span>设备 / 属性或动作</span><span>期望值 / 参数</span><span>状态</span><span>更新时间</span></div>{commands.map((command) => <div className="command-row" key={command.id}><span><b>{command.deviceId}</b><small>{command.capabilityId}.{command.commandId ?? command.propertyId}</small></span><code>{expectedValue(command)}</code><span><i className={`command-status is-${command.status}`}>{command.status}</i>{command.error && <small>{command.error}</small>}</span><time>{new Date(command.updatedAt).toLocaleString()}</time></div>)}</div>}</div>
  </section>
}
