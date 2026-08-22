import { useEffect, useMemo, useRef, useState } from 'react'
import type { AuditEvent, DeviceCommand, Diagnostics, RuntimeSettings, SubprocessLogEntry } from '../types/diagnostics'
import { listRuntimeLogs, runtimeLogRecoveryLimit } from '../api/diagnostics'
import { subscribeEvents } from '../api/events'
import { downloadDatabaseBackup, getMasterKeyStatus, rotateMasterKey, stageDatabaseRestore, type MasterKeyStatus } from '../api/maintenance'
import { confirmExactPhrase } from '../confirmations'


const LIST_PAGE_SIZE_OPTIONS = [10, 20, 50] as const
const DEFAULT_LIST_PAGE_SIZE = 20

function clampPage(page: number, totalPages: number): number {
  return Math.min(Math.max(page, 1), Math.max(totalPages, 1))
}

function paginateItems<T>(items: T[], page: number, pageSize: number): { page: number; totalPages: number; totalItems: number; start: number; end: number; pageItems: T[] } {
  const totalItems = items.length
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize) || 1)
  const currentPage = clampPage(page, totalPages)
  const startIndex = totalItems === 0 ? 0 : (currentPage - 1) * pageSize
  const endIndex = Math.min(startIndex + pageSize, totalItems)
  return {
    page: currentPage,
    totalPages,
    totalItems,
    start: totalItems === 0 ? 0 : startIndex + 1,
    end: endIndex,
    pageItems: items.slice(startIndex, endIndex),
  }
}

function ListPagination({
  label,
  page,
  totalPages,
  totalItems,
  start,
  end,
  pageSize,
  onPageChange,
  onPageSizeChange,
}: {
  label: string
  page: number
  totalPages: number
  totalItems: number
  start: number
  end: number
  pageSize: number
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
}) {
  return (
    <div className="list-pagination" role="navigation" aria-label={`${label}分页`}>
      <label className="list-pagination__size">
        每页
        <select aria-label={`${label}每页条数`} value={pageSize} onChange={(event) => onPageSizeChange(Number(event.target.value))}>
          {LIST_PAGE_SIZE_OPTIONS.map((size) => <option key={size} value={size}>{size} 条</option>)}
        </select>
      </label>
      <span className="list-pagination__summary" role="status">
        {totalItems === 0 ? '暂无记录' : `第 ${start}–${end} 条 / 共 ${totalItems} 条 · 第 ${page} / ${totalPages} 页`}
      </span>
      <div className="list-pagination__controls">
        <button type="button" disabled={page <= 1} onClick={() => onPageChange(page - 1)}>上一页</button>
        <button type="button" disabled={page >= totalPages} onClick={() => onPageChange(page + 1)}>下一页</button>
      </div>
    </div>
  )
}

function expectedValue(command: DeviceCommand): string { if (command.kind === 'action') return Object.entries(command.parameters ?? {}).map(([key, value]) => `${key}=${value.bool ?? value.int ?? value.number ?? value.string ?? '—'}`).join(', ') || '无参数'; const value = command.expected; if (!value) return '—'; if (value.bool !== undefined) return String(value.bool); if (value.int !== undefined) return String(value.int); if (value.number !== undefined) return String(value.number); return value.string ?? '—' }
function megabytes(bytes: number): string { return `${(bytes / 1024 / 1024).toFixed(1)}MB` }

function RuntimeSettingsCard({ settings, onSave }: { settings: RuntimeSettings | null; onSave: (settings: RuntimeSettings) => Promise<void> }) {
  const [seconds, setSeconds] = useState(settings?.commandTimeoutSeconds ?? 5)
  const [historyLimit, setHistoryLimit] = useState(settings?.commandHistoryLimit ?? 1000)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { if (settings) { setSeconds(settings.commandTimeoutSeconds); setHistoryLimit(settings.commandHistoryLimit) } }, [settings])
  const save = async () => { if (seconds < 1 || seconds > 300) { setError('超时请输入 1–300 秒'); return }; if (historyLimit < 100 || historyLimit > 10000) { setError('历史上限请输入 100–10000'); return }; setSaving(true); setError(null); try { await onSave({ commandTimeoutSeconds: seconds, commandHistoryLimit: historyLimit }) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败') } finally { setSaving(false) } }
  return <div className="queue-card settings-card"><div><span>命令运行时设置</span><strong>{settings?.commandTimeoutSeconds ?? seconds}s / {settings?.commandHistoryLimit ?? historyLimit} 条</strong></div><label>确认超时秒数<input aria-label="命令确认超时秒数" type="number" min="1" max="300" value={seconds} onChange={(event) => setSeconds(Number(event.target.value))} /></label><label>历史保留上限<input aria-label="命令历史保留上限" type="number" min="100" max="10000" value={historyLimit} onChange={(event) => setHistoryLimit(Number(event.target.value))} /></label><button className="primary" disabled={saving || !settings} onClick={() => void save()}>{saving ? '保存中…' : '保存并实时应用'}</button>{error && <small className="field-error" role="alert">{error}</small>}<small>存储于数据库。超时仅影响之后创建的命令；降低历史上限会立即清理最旧的终态记录，不删除执行中的命令。</small></div>
}

function DatabaseMaintenanceCard() {
	const [restoreFile, setRestoreFile] = useState<File | null>(null)
	const [keyStatus, setKeyStatus] = useState<MasterKeyStatus | null>(null)
	const [busy, setBusy] = useState<'backup' | 'restore' | 'key' | 'key-status' | null>(null)
	const [message, setMessage] = useState<string | null>(null)
	const [error, setError] = useState<string | null>(null)

	async function backup() {
		const confirmation = confirmExactPhrase('完整备份包含数据库主密钥、Provider 凭据和桥 PIN，请按敏感文件保管。', 'BACKUP')
		if (!confirmation) return
		setBusy('backup'); setError(null); setMessage(null)
		try {
			const result = await downloadDatabaseBackup(confirmation)
			const url = URL.createObjectURL(result.blob)
			const anchor = document.createElement('a')
			anchor.href = url; anchor.download = result.filename; anchor.click()
			URL.revokeObjectURL(url)
			setMessage('完整数据库逻辑快照已生成。HomeKit 控制器配对文件不在此压缩包内。')
		} catch (cause) { setError(cause instanceof Error ? cause.message : '生成备份失败') } finally { setBusy(null) }
	}

	async function restore() {
		if (!restoreFile) { setError('请先选择 HomeLoom 备份压缩包'); return }
		const confirmation = confirmExactPhrase('恢复会在下次启动前替换整库，并保留当前数据库的恢复前快照。', 'RESTORE')
		if (!confirmation) return
		setBusy('restore'); setError(null); setMessage(null)
		try {
			const result = await stageDatabaseRestore(restoreFile, confirmation)
			setMessage(`备份已验证并暂存（schema v${result.schemaVersion}）。请正常重启 HomeLoom 以应用恢复。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '暂存恢复失败') } finally { setBusy(null) }
	}

	async function rotateKey() {
		const resume = Boolean(keyStatus?.needsReencryption)
		const confirmation = confirmExactPhrase(resume ? '上次密钥轮换未完成。将使用当前活动密钥安全重试批量重加密。' : '轮换会生成新的数据库主密钥并批量重加密所有持久化凭据。旧密钥会保留在受限 keyring 中，以支持旧备份恢复；请先下载完整备份。', 'ROTATE')
		if (!confirmation) return
		setBusy('key'); setError(null); setMessage(null)
		try {
			const result = await rotateMasterKey(confirmation, resume)
			setKeyStatus(result.status)
			setMessage(`主密钥已切换至 v${result.activeVersion}，已重加密 ${result.reencrypted} 项持久化秘密。旧版本仅保留用于恢复旧备份。`)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '主密钥轮换失败') } finally { setBusy(null) }
	}

	async function refreshKeyStatus() {
		setBusy('key-status'); setError(null)
		try {
			const status = await getMasterKeyStatus()
			if (!isMasterKeyStatus(status)) throw new Error('主密钥状态响应无效')
			setKeyStatus(status)
		} catch (cause) { setError(cause instanceof Error ? cause.message : '读取主密钥状态失败') } finally { setBusy(null) }
	}

	return <div className="maintenance-card queue-card"><div><span>数据库备份、恢复与密钥</span><strong>单管理员运维</strong></div><p>完整备份包含数据库逻辑快照、管理员密码哈希、加密配置以及数据库主密钥。恢复包会先进行格式、Schema 和密钥校验，再以事务替换当前数据。</p><div className="maintenance-actions"><button className="primary" disabled={busy !== null} onClick={() => void backup()}>{busy === 'backup' ? '生成中…' : '下载完整备份'}</button><label className="file-picker">恢复压缩包<span>{restoreFile ? restoreFile.name : '选择 .zip 备份包'}</span><input aria-label="恢复压缩包" type="file" accept=".zip,application/zip" onChange={(event) => setRestoreFile(event.target.files?.[0] ?? null)} /></label><button className="is-danger" disabled={busy !== null || !restoreFile} onClick={() => void restore()}>{busy === 'restore' ? '校验中…' : '校验并暂存恢复'}</button></div><div className="maintenance-actions master-key-actions"><span>{keyStatus ? `主密钥 v${keyStatus.activeVersion} · 保留 v${keyStatus.retainedVersions.join('、v')}` : '请先读取主密钥状态'}</span><button disabled={busy !== null} onClick={() => void refreshKeyStatus()}>{busy === 'key-status' ? '读取中…' : '读取主密钥状态'}</button><button className="is-danger" disabled={busy !== null || !keyStatus} onClick={() => void rotateKey()}>{busy === 'key' ? '轮换中…' : keyStatus?.needsReencryption ? '恢复密钥轮换' : '轮换主密钥'}</button></div>{keyStatus?.needsReencryption && <small className="field-error" role="alert">检测到旧版本密文；请使用“恢复密钥轮换”完成批量重加密。</small>}{message && <small className="maintenance-message" role="status">{message}</small>}{error && <small className="field-error" role="alert">{error}</small>}<small>整库恢复需要一次正常进程重启；主密钥轮换不删除旧密钥，旧版本仅为恢复历史备份而保留。</small></div>
}

function isMasterKeyStatus(value: unknown): value is MasterKeyStatus {
	if (!value || typeof value !== 'object') return false
	const status = value as Partial<MasterKeyStatus>
	return typeof status.activeVersion === 'number' && Array.isArray(status.retainedVersions) && typeof status.needsReencryption === 'boolean'
}

function mergeRuntimeLogs(current: SubprocessLogEntry[], incoming: SubprocessLogEntry[]): SubprocessLogEntry[] {
	const bySequence = new Map(current.map((entry) => [entry.sequence, entry]))
	for (const entry of incoming) bySequence.set(entry.sequence, entry)
	return [...bySequence.values()].sort((left, right) => left.sequence - right.sequence).slice(-2000)
}

function RuntimeLogDialog({ onClose }: { onClose: () => void }) {
	const [entries, setEntries] = useState<SubprocessLogEntry[]>([])
	const [query, setQuery] = useState('')
	const [process, setProcess] = useState('all')
	const [error, setError] = useState<string | null>(null)
	const [retainedWindowNotice, setRetainedWindowNotice] = useState<string | null>(null)
	const cursor = useRef(0)

	useEffect(() => {
		const previousOverflow = document.body.style.overflow
		document.body.style.overflow = 'hidden'
		const closeOnEscape = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
		window.addEventListener('keydown', closeOnEscape)
		return () => {
			document.body.style.overflow = previousOverflow
			window.removeEventListener('keydown', closeOnEscape)
		}
	}, [onClose])

	useEffect(() => {
		let active = true
		const controller = new AbortController()
		let recovery: Promise<void> | null = null
		let recoveryAfter: number | null = null
		const append = (next: SubprocessLogEntry[], detectLiveGap = true) => {
			if (!active || next.length === 0) return
			const ordered = [...next].sort((left, right) => left.sequence - right.sequence)
			const previousCursor = cursor.current
			const firstAfterCursor = ordered.find((entry) => entry.sequence > previousCursor)
			if (detectLiveGap && firstAfterCursor && firstAfterCursor.sequence > previousCursor + 1) requestRecovery(previousCursor)
			cursor.current = Math.max(cursor.current, ...next.map((entry) => entry.sequence))
			setEntries((current) => mergeRuntimeLogs(current, next))
		}
		const scheduleRecovery = (after: number) => {
			recoveryAfter = recoveryAfter === null ? after : Math.min(recoveryAfter, after)
		}
		const requestRecovery = (after = cursor.current): Promise<void> => {
			scheduleRecovery(after)
			if (recovery) return recovery
			recovery = (async () => {
				while (active && recoveryAfter !== null) {
					const requestedAfter = recoveryAfter
					recoveryAfter = null
					try {
						const history = await listRuntimeLogs(requestedAfter, controller.signal)
						if (!active) return
						const first = history[0]
						if (first && first.sequence > requestedAfter + 1) {
							setRetainedWindowNotice(`序号 ${requestedAfter + 1} 至 ${first.sequence - 1} 的较早运行日志已超出 2000 条保留窗口。`)
						}
						setError(null)
						append(history, false)
						// A full response can have raced a busy stream. Follow the
						// cursor once more, never by timer or once per live entry.
						if (history.length === runtimeLogRecoveryLimit) scheduleRecovery(cursor.current)
					} catch (cause) {
						if (active && !(cause instanceof DOMException && cause.name === 'AbortError')) setError(cause instanceof Error ? cause.message : '读取运行日志失败')
						return
					}
				}
			})().finally(() => { recovery = null })
			return recovery
		}
		// Subscribe before reading the retained snapshot so a log emitted during
		// the initial request is de-duplicated instead of lost.
		const unsubscribe = subscribeEvents({
			onReady: () => { if (!recovery) void requestRecovery() },
			onRuntimeLog: (entry) => append([entry]),
			onRuntimeLogGap: () => { void requestRecovery() },
		})
		void requestRecovery(0)
		return () => { active = false; recoveryAfter = null; controller.abort(); unsubscribe() }
	}, [])

	const processes = useMemo(() => [...new Set(entries.map((entry) => entry.subsystem ?? entry.process))].sort(), [entries])
	const normalized = query.trim().toLowerCase()
	const filtered = entries.filter((entry) => (process === 'all' || (entry.subsystem ?? entry.process) === process) && (!normalized || `${entry.process} ${entry.component ?? ''} ${entry.module ?? ''} ${entry.facility ?? ''} ${entry.subsystem ?? ''} ${entry.instance} ${entry.level ?? ''} ${entry.message} ${entry.error ?? ''}`.toLowerCase().includes(normalized)))
	return <div className="modal-backdrop subprocess-log-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose() }}><section className="subprocess-log-dialog" role="dialog" aria-modal="true" aria-label="运行日志管理">
		<div className="command-heading"><div><p className="eyebrow">运行诊断（RUNTIME DIAGNOSTICS）</p><h3>实时运行日志</h3></div><div className="subprocess-log-heading-actions"><span>主进程与子进程 · SSE 自动重连 · 内存最多 2000 条</span><button type="button" onClick={onClose}>关闭</button></div></div>
		<div className="command-filters subprocess-log-filters"><input aria-label="搜索运行日志" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索进程、实例、等级或内容" /><select aria-label="进程类型" value={process} onChange={(event) => setProcess(event.target.value)}><option value="all">全部进程</option>{processes.map((item) => <option key={item} value={item}>{subprocessLabel(item)}</option>)}</select><span>{filtered.length} / {entries.length}</span></div>
		{retainedWindowNotice && <div className="maintenance-message" role="status">{retainedWindowNotice}</div>}
		{error && <div className="command-empty" role="alert">{error}</div>}
		{!error && filtered.length === 0 ? <div className="command-empty">暂无运行日志</div> : <div className="subprocess-log-view" role="log" aria-label="运行日志内容">{filtered.map((entry) => <div key={entry.sequence}><time>{new Date(entry.time).toLocaleTimeString()}</time><b className={`is-${entry.level ?? 'unknown'}`}>{entry.level ?? 'log'}</b><span>{subprocessLabel(entry.subsystem ?? entry.process)}/{entry.instance}</span><code>{entry.message}{entry.error ? ` · ${entry.error}` : ''}</code></div>)}</div>}
	</section></div>
}

function subprocessLabel(value: string): string {
	if (value === 'homekit') return 'HomeKit'
	if (value === 'ffmpeg') return 'FFmpeg'
	if (value === 'camera-kernel') return 'Camera Kernel'
	if (value === 'matter') return 'Matter'
	if (value === 'backend') return 'HomeLoom'
	return value
}

export function SystemDashboard({ diagnostics, commands, auditEvents = [], settings, onSettingsSave }: { diagnostics: Diagnostics | null; commands: DeviceCommand[]; auditEvents?: AuditEvent[]; settings: RuntimeSettings | null; onSettingsSave: (settings: RuntimeSettings) => Promise<void> }) {
	const [commandQuery, setCommandQuery] = useState('')
	const [commandStatus, setCommandStatus] = useState('all')
	const [commandPage, setCommandPage] = useState(1)
	const [commandPageSize, setCommandPageSize] = useState<number>(DEFAULT_LIST_PAGE_SIZE)
	const [auditQuery, setAuditQuery] = useState('')
	const [auditPage, setAuditPage] = useState(1)
	const [auditPageSize, setAuditPageSize] = useState<number>(DEFAULT_LIST_PAGE_SIZE)
	const [subprocessLogsOpen, setSubprocessLogsOpen] = useState(false)

  const filteredCommands = useMemo(() => {
    const normalizedQuery = commandQuery.trim().toLowerCase()
    return commands.filter((command) => {
      const matchesStatus = commandStatus === 'all' || command.status === commandStatus || command.outcome === commandStatus
      const searchable = `${command.id} ${command.correlationId ?? ''} ${command.deviceId} ${command.endpointId} ${command.capabilityId} ${command.propertyId ?? ''} ${command.commandId ?? ''} ${command.error ?? ''}`.toLowerCase()
      return matchesStatus && (!normalizedQuery || searchable.includes(normalizedQuery))
    })
  }, [commandQuery, commandStatus, commands])

  const filteredAuditEvents = useMemo(() => {
    const normalizedAuditQuery = auditQuery.trim().toLowerCase()
    return auditEvents.filter((event) => !normalizedAuditQuery || `${event.correlationId} ${event.action} ${event.resourceType} ${event.resourceId ?? ''} ${event.method} ${event.route} ${event.outcome}`.toLowerCase().includes(normalizedAuditQuery))
  }, [auditEvents, auditQuery])

  const commandPagination = useMemo(() => paginateItems(filteredCommands, commandPage, commandPageSize), [commandPage, commandPageSize, filteredCommands])
  const auditPagination = useMemo(() => paginateItems(filteredAuditEvents, auditPage, auditPageSize), [auditPage, auditPageSize, filteredAuditEvents])

  useEffect(() => { setCommandPage(1) }, [commandQuery, commandStatus, commandPageSize])
  useEffect(() => { setAuditPage(1) }, [auditQuery, auditPageSize])
  useEffect(() => { if (commandPage !== commandPagination.page) setCommandPage(commandPagination.page) }, [commandPage, commandPagination.page])
  useEffect(() => { if (auditPage !== auditPagination.page) setAuditPage(auditPagination.page) }, [auditPage, auditPagination.page])

  if (!diagnostics) return <div className="empty-state" role="status">正在加载诊断数据…</div>
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
	['数据库操作', diagnostics.databaseOperations],
	['数据库平均延迟', `${diagnostics.databaseAverageLatencyMs.toFixed(1)}ms`],
	['数据库最大延迟', `${diagnostics.databaseMaxLatencyMs.toFixed(1)}ms`],
	['Provider 事件时钟漂移', diagnostics.providerClockSkewEvents],
	['事件最大时钟偏差', `${diagnostics.providerMaxClockSkewMs.toFixed(0)}ms`],
	['事件偏差来源', diagnostics.providerClockSkewSource || '—'],
	['Provider 刷新旧快照', diagnostics.providerSnapshotAgeEvents ?? 0],
	['最大快照年龄', `${(diagnostics.providerMaxSnapshotAgeMs ?? 0).toFixed(0)}ms`],
	['旧快照来源', diagnostics.providerSnapshotAgeSource || '—'],
	['忽略乱序/重复事件', diagnostics.providerEventsIgnored ?? 0],
	['Provider 消息', diagnostics.providerMessagesReceived ?? 0],
	['Provider 无效消息', diagnostics.providerMessagesInvalid ?? 0],
	['Provider 队列丢弃', diagnostics.providerMessagesDropped ?? 0],
	['Provider 已发命令', diagnostics.providerCommandsPublished ?? 0],
	['属性映射命中', diagnostics.mappingApplied ?? 0],
	['属性映射失败', diagnostics.mappingErrors ?? 0],
  ]
  return <section className="system-dashboard"><div className="metric-grid">{metrics.map(([label, value]) => <article key={label}><span>{label}</span><strong>{value}</strong></article>)}</div>
    <div className="queue-card"><div><span>事件队列</span><strong>{diagnostics.eventQueuePending} / {diagnostics.eventQueueCapacity}</strong></div><div className="queue-track"><span style={{ width: `${Math.min(queueRate, 100)}%` }} /></div><small>当前占用 {queueRate}% · 核心队列满时会丢弃并计数，不阻塞 Provider 线程。</small></div>
    <RuntimeSettingsCard settings={settings} onSave={onSettingsSave} />
	<div className="artifact-card queue-card"><div><span>支持资料</span><strong>已自动脱敏</strong></div><p>配置导出不包含桥 PIN、Setup URI 或本地存储路径；Provider 凭据会替换为星号。诊断包额外包含版本、运行指标和最近审计记录。</p><div className="artifact-actions"><a className="ui-button" href="/api/v1/system/config-export" download>导出脱敏配置</a><a className="ui-button is-primary" href="/api/v1/system/diagnostic-bundle" download>下载诊断包</a></div><small>下载响应禁止浏览器缓存，分享前仍建议检查设备名称等非凭据数据。</small></div>
	<DatabaseMaintenanceCard />
	<div className="subprocess-log-launcher queue-card"><div><span>实时运行日志</span><strong>HomeLoom · Camera Kernel · Matter</strong></div><p>主进程与子进程输出均由主程序集中采集并脱敏。日志通过 SSE 实时推送；断线后会按游标补回近期记录。</p><button className="primary" type="button" onClick={() => setSubprocessLogsOpen(true)}>打开日志窗口</button></div>
	{subprocessLogsOpen && <RuntimeLogDialog onClose={() => setSubprocessLogsOpen(false)} />}
	<div className="audit-section command-section"><div className="command-heading"><h3>实时审计日志</h3><span>数据库持久化最近 5000 条 · 页面加载最近 200 条</span></div><div className="command-filters"><input aria-label="搜索审计日志" value={auditQuery} onChange={(event) => setAuditQuery(event.target.value)} placeholder="搜索资源、动作、路由或 correlation ID" /><span>{filteredAuditEvents.length} / {auditEvents.length}</span></div>{auditEvents.length === 0 ? <div className="command-empty">还没有管理操作记录</div> : filteredAuditEvents.length === 0 ? <div className="command-empty">没有匹配的审计记录</div> : <><div className="audit-table"><div className="audit-row command-header"><span>资源 / 动作</span><span>结果</span><span>Correlation ID</span><span>时间</span></div>{auditPagination.pageItems.map((event) => <div className="audit-row" key={event.id}><span><b>{event.resourceType}{event.resourceId ? ` · ${event.resourceId}` : ''}</b><small>{event.method} {event.route} · {event.action}</small></span><span><i className={`command-status is-${event.outcome === 'succeeded' ? 'confirmed' : 'rejected'}`}>{event.status}</i><small>{event.outcome}</small></span><code title={event.correlationId}>{event.correlationId}</code><time>{new Date(event.createdAt).toLocaleString()}</time></div>)}</div><ListPagination label="审计日志" page={auditPagination.page} totalPages={auditPagination.totalPages} totalItems={auditPagination.totalItems} start={auditPagination.start} end={auditPagination.end} pageSize={auditPageSize} onPageChange={setAuditPage} onPageSizeChange={setAuditPageSize} /></>}</div>
    <div className="command-section"><div className="command-heading"><h3>命令历史</h3><span>内存中最多保留 {settings?.commandHistoryLimit ?? 1000} 条记录</span></div><div className="command-filters"><input aria-label="搜索命令" value={commandQuery} onChange={(event) => setCommandQuery(event.target.value)} placeholder="搜索设备、属性、动作、错误或命令 ID" /><select aria-label="命令状态" value={commandStatus} onChange={(event) => setCommandStatus(event.target.value)}><option value="all">全部状态</option><option value="queued">queued</option><option value="sent">sent</option><option value="accepted">accepted</option><option value="confirmed">confirmed</option><option value="rejected">rejected</option><option value="timeout">timeout</option><option value="superseded">superseded</option><option value="unknown">outcome unknown</option></select><span>{filteredCommands.length} / {commands.length}</span></div>{commands.length === 0 ? <div className="command-empty">还没有控制命令</div> : filteredCommands.length === 0 ? <div className="command-empty">没有匹配的命令</div> : <><div className="command-table"><div className="command-row command-header"><span>设备 / 属性或动作</span><span>期望值 / 参数</span><span>状态 / 结果</span><span>更新时间</span></div>{commandPagination.pageItems.map((command) => <div className="command-row" key={command.id}><span><b>{command.deviceId}</b><small>{command.capabilityId}.{command.commandId ?? command.propertyId}</small><small>{command.id}</small>{command.correlationId && <small>trace: {command.correlationId}</small>}{Boolean(command.coalescedRequests) && <small>合并重复请求 × {command.coalescedRequests}</small>}</span><code>{expectedValue(command)}</code><span><i className={`command-status is-${command.status}`}>{command.status}</i>{command.outcome && <small>outcome: {command.outcome}</small>}{command.error && <small>{command.error}</small>}</span><time>{new Date(command.updatedAt).toLocaleString()}</time></div>)}</div><ListPagination label="命令历史" page={commandPagination.page} totalPages={commandPagination.totalPages} totalItems={commandPagination.totalItems} start={commandPagination.start} end={commandPagination.end} pageSize={commandPageSize} onPageChange={setCommandPage} onPageSizeChange={setCommandPageSize} /></>}</div>
  </section>
}
