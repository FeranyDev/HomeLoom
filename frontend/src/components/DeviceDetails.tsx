import { useCallback, useEffect, useRef, useState } from 'react'
import { getDeviceProperty, getDeviceStates, subscribeDeviceStates } from '../api/devices'
import { availabilityLabel, type Device, type PropertyValue, type StateValue } from '../types/device'
import { PropertyControl } from './PropertyControl'
import { CommandControl } from './CommandControl'
import { bilingual, deviceTypeLabel, parameterLevelLabel, permissionLabel, propertyDisplayLabel, resourceLabel } from '../presentationLabels'
import { HelpTooltip } from './HelpTooltip'

function displayValue(value: PropertyValue): string { if (value.bool !== undefined) return value.bool ? 'true' : 'false'; if (value.int !== undefined) return String(value.int); if (value.number !== undefined) return String(value.number); return value.string ?? '—' }
function mergeState(items: StateValue[], incoming: StateValue): StateValue[] { const index = items.findIndex((item) => item.key.endpointId === incoming.key.endpointId && item.key.capabilityId === incoming.key.capabilityId && item.key.propertyId === incoming.key.propertyId); if (index < 0) return [...items, incoming]; if (items[index].version > incoming.version) return items; return items.map((item, current) => current === index ? incoming : item) }

function CameraPreview({ deviceId }: { deviceId: string }) {
  const [attempt, setAttempt] = useState(0)
  const [status, setStatus] = useState<'loading' | 'playing' | 'buffering' | 'error' | 'timeout'>('loading')
  const videoRef = useRef<HTMLVideoElement>(null)
  const hasPlayed = useRef(false)
  const source = `/api/v1/media/devices/${encodeURIComponent(deviceId)}/preview.mp4?attempt=${attempt}`
  const retry = () => { setStatus('loading'); setAttempt((current) => current + 1) }
  const followLiveEdge = useCallback((video: HTMLVideoElement) => {
    if (video.buffered.length === 0) return
    const liveEdge = video.buffered.end(video.buffered.length - 1)
    if (liveEdge - video.currentTime > 1.5) video.currentTime = Math.max(0, liveEdge - 0.25)
  }, [])
  const play = useCallback((video: HTMLVideoElement) => {
    followLiveEdge(video)
    const pending = video.play()
    if (pending) void pending.catch(() => {
      if (videoRef.current === video && video.dataset.previewAttempt === String(attempt)) setStatus('error')
    })
  }, [attempt, followLiveEdge])
  useEffect(() => {
    const video = videoRef.current
    if (!video) return
    hasPlayed.current = false
    setStatus('loading')
    // Assigning explicitly and calling load/play avoids relying on a browser
    // retrying an autoplay attempt made before the fragmented MP4 init arrived.
    video.dataset.previewAttempt = String(attempt)
    video.setAttribute('src', source)
    video.load()
    play(video)
    return () => {
      video.pause()
      video.removeAttribute('src')
      delete video.dataset.previewAttempt
      video.load()
    }
  }, [attempt, play, source])
  useEffect(() => {
    if (status !== 'loading' || hasPlayed.current) return
    const timer = window.setTimeout(() => setStatus('timeout'), 12_000)
    return () => window.clearTimeout(timer)
  }, [attempt, status])
  return <section className="camera-preview" aria-label="摄像头实时预览">
    <div className="camera-preview-heading"><div><span>媒体诊断（MEDIA DIAGNOSTICS）</span><strong><HelpTooltip content="预览由登录保护的本机代理提供，不会向浏览器暴露 Token 或发布器地址。" label="摄像头预览说明">摄像头实时画面</HelpTooltip></strong></div><button onClick={retry}>重新连接</button></div>
    <div className={`camera-preview-stage is-${status}`}>
      <video ref={videoRef} autoPlay muted playsInline controls preload="auto" aria-label="摄像头实时画面"
        onLoadedMetadata={(event) => play(event.currentTarget)}
        onCanPlay={(event) => play(event.currentTarget)}
        onPlaying={(event) => { hasPlayed.current = true; followLiveEdge(event.currentTarget); setStatus('playing') }}
        onProgress={(event) => followLiveEdge(event.currentTarget)}
        onTimeUpdate={(event) => followLiveEdge(event.currentTarget)}
        onWaiting={() => setStatus(hasPlayed.current ? 'buffering' : 'loading')}
        onStalled={() => setStatus(hasPlayed.current ? 'buffering' : 'loading')}
        onError={() => setStatus('error')}
        onEnded={() => setStatus('error')} />
      {status === 'loading' && <p>正在向独立媒体发布器请求视频流…</p>}
      {status === 'buffering' && <span className="camera-preview-buffering" role="status">正在追赶实时画面…</span>}
      {status === 'timeout' && <p className="inline-error" role="alert">等待关键帧超时。请点击“重新连接”；HomeLoom 已中止本次无响应的预览请求。</p>}
      {status === 'error' && <p className="inline-error" role="alert">实时预览已中断。请点击“重新连接”；该结果通常表示取流、H.264 解码或浏览器播放失败，而非 HomeKit 绑定失败。</p>}
    </div>
  </section>
}

export function DeviceDetails({ device, onClose, onPropertyWrite, onCommandExecute }: { device: Device; onClose: () => void; onPropertyWrite: (endpointId: string, capabilityId: string, propertyId: string, value: PropertyValue) => Promise<void>; onCommandExecute: (endpointId: string, capabilityId: string, commandId: string, parameters: Record<string, PropertyValue>, idempotencyKey: string) => Promise<void> }) {
  const [states, setStates] = useState<StateValue[]>([])
  const [readValues, setReadValues] = useState<Record<string, PropertyValue>>({})
  const [reading, setReading] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { const controller = new AbortController(); void getDeviceStates(device.id, controller.signal).then((items) => { setStates((current) => items.reduce(mergeState, current)); setError(null) }).catch((cause) => { if (!(cause instanceof DOMException && cause.name === 'AbortError')) setError(cause instanceof Error ? cause.message : '状态读取失败') }); const unsubscribe = subscribeDeviceStates(device.id, (state) => setStates((current) => mergeState(current, state))); return () => { controller.abort(); unsubscribe() } }, [device.id])
  useEffect(() => { const close = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }; window.addEventListener('keydown', close); return () => window.removeEventListener('keydown', close) }, [onClose])
  const endpoints = device.endpoints ?? []
  const controlStates = states.filter((item) => item.providerId !== device.providerId)
  const unavailableControlStates = controlStates.filter((item) => item.available === false)
  const degradedControlProviders = [...new Set(unavailableControlStates.map((item) => item.providerId))]
  const stateFor = (endpointId: string, capabilityId: string, propertyId: string) => states.find((item) => item.key.endpointId === endpointId && item.key.capabilityId === capabilityId && item.key.propertyId === propertyId)
  const readProperty = async (endpointId: string, capabilityId: string, propertyId: string) => { const key = `${endpointId}/${capabilityId}/${propertyId}`; setReading(key); try { const property = await getDeviceProperty(device.id, endpointId, capabilityId, propertyId); setReadValues((current) => ({ ...current, [key]: property.value })); setError(null) } catch (cause) { setError(cause instanceof Error ? cause.message : '属性读取失败') } finally { setReading(null) } }
  const writeMappedProperty = (endpointId: string, capabilityId: string, property: { definition: { id: string } }, value: PropertyValue) => onPropertyWrite(endpointId, capabilityId, property.definition.id, value)
  return <div className="modal-backdrop"><section className="device-details" role="dialog" aria-modal="true" aria-label={`${device.name}详情`}><div className="form-heading"><div><p className="eyebrow">统一设备模型（UNIFIED DEVICE MODEL）· {deviceTypeLabel(device.type)}</p><h2>{device.name}</h2><small>{device.providerId} · {device.id}</small></div><button onClick={onClose}>关闭</button></div>
    <div className="device-detail-summary"><span><i className={`status-dot is-${device.availability}`} />{device.removed ? '来源已删除' : device.disabled ? '已禁用' : availabilityLabel(device.availability)}</span><span>{deviceTypeLabel(device.type)}</span><span>更新于 {new Date(device.lastUpdateAt).toLocaleString()}</span></div>
    {error && <p className="inline-error">{error}</p>}
    {device.type === 'camera' && <CameraPreview deviceId={device.id} />}
    {device.type === 'camera' && device.online && unavailableControlStates.length > 0 && <p className="camera-control-degraded" role="status">
      <strong>视频可用，部分控制暂不可用。</strong>
      <span>Camera Provider 的媒体链路仍在线；{degradedControlProviders.join('、')} 返回了 {unavailableControlStates.length} 个不可用属性。可继续查看画面，相关控制会在 Xiaomi Provider 恢复后自动可用。</span>
    </p>}
    <div className="capability-tree">{endpoints.length === 0 && <p>该设备没有可配置的统一属性；摄像头媒体由独立发布器管理。</p>}{endpoints.map((endpoint) => <section key={endpoint.id}><div className="tree-heading"><span>端点（ENDPOINT）</span><strong>{endpoint.name} · {resourceLabel(endpoint.id)}</strong><code>{endpoint.id} · {endpoint.type}</code></div>{(endpoint.capabilities ?? []).map((capability) => <div className="capability-node" key={capability.id}><div><span>能力（CAPABILITY）</span><strong>{resourceLabel(capability.type)}</strong><code>{capability.id}</code></div>{(capability.properties ?? []).map((property) => { const state = stateFor(endpoint.id, capability.id, property.definition.id); const key = `${endpoint.id}/${capability.id}/${property.definition.id}`; const value = readValues[key] ?? property.value; const unknown = state?.known === false; const level = property.definition.parameterLevel ?? 'custom'; return <article className="property-node" key={property.definition.id}><div><strong>{propertyDisplayLabel(property.definition.name, property.definition.id)}</strong><code>{property.definition.id}</code></div><div className="property-value"><b>{unknown ? '无历史值' : displayValue(value)}{!unknown && property.definition.unit ? ` ${property.definition.unit}` : ''}</b><span>{permissionLabel(property.definition.readable, property.definition.writable, property.definition.notifiable)}</span>{property.definition.readable && <button disabled={!device.online || reading === key} onClick={() => void readProperty(endpoint.id, capability.id, property.definition.id)}>{reading === key ? '读取中…' : '从提供端（Provider）读取'}</button>}</div><span className={`parameter-level is-${level}`}>{parameterLevelLabel(level)}属性</span><PropertyControl definition={property.definition} value={value} disabled={!device.online || state?.available === false} onWrite={(value) => writeMappedProperty(endpoint.id, capability.id, property, value)} /><dl><div><dt>状态来源</dt><dd>{state?.providerId ?? device.providerId} · {state?.source ?? '—'}</dd></div><div><dt>质量（quality）</dt><dd><i className={`quality is-${state?.quality ?? 'unknown'}`}>{bilingual(state?.quality ?? 'unknown', state?.quality === 'good' ? '正常' : state?.quality === 'stale' ? '过期' : state?.quality === 'pending' ? '待确认' : '未知')}</i></dd></div><div><dt>可用性（available）</dt><dd>{state ? state.available ? '可用（available）' : bilingual(state.unavailableReason ?? 'unavailable', '不可用') : '—'}</dd></div><div><dt>版本（version）</dt><dd>{state?.version ?? '—'} / 序列（seq）{state?.sequence ?? '—'}</dd></div><div><dt>追踪标识（Trace）</dt><dd>{state?.traceId ?? '—'}</dd></div><div><dt>观察时间</dt><dd>{state ? new Date(state.observedAt).toLocaleString() : '—'}</dd></div><div><dt>生存时间（TTL）</dt><dd>{property.definition.staleAfterSeconds ? `${property.definition.staleAfterSeconds}s` : '无'}{state?.expiresAt ? ` · ${new Date(state.expiresAt).toLocaleTimeString()}` : ''}</dd></div><div><dt>待确认命令</dt><dd>{state?.pendingCommandId ?? '无'}</dd></div></dl></article>})}{capability.commands?.length ? <div className="command-controls">{capability.commands.map((command) => <CommandControl key={command.id} definition={command} onExecute={(parameters, idempotencyKey) => onCommandExecute(endpoint.id, capability.id, command.id, parameters, idempotencyKey)} />)}</div> : null}</div>)}</section>)}</div>
  </section></div>
}
