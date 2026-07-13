import { useCallback, useEffect, useState } from 'react'
import { listDevices, setDevicePower, simulateDevice, subscribeDevices } from './api/devices'
import { deleteTarget, listTargets, saveTarget } from './api/targets'
import { deleteProvider, listProviders, saveProvider } from './api/providers'
import { DeviceCard } from './components/DeviceCard'
import { TargetCard } from './components/TargetCard'
import { TargetForm } from './components/TargetForm'
import { ProviderCard } from './components/ProviderCard'
import { ProviderForm } from './components/ProviderForm'
import type { Device } from './types/device'
import type { Target, TargetInput } from './types/target'
import type { Provider, ProviderInput } from './types/provider'

type Page = 'devices' | 'providers' | 'targets'

export function App() {
  const [devices, setDevices] = useState<Device[]>([])
	const [targets, setTargets] = useState<Target[]>([])
	const [providers, setProviders] = useState<Provider[]>([])
	const [page, setPage] = useState<Page>('devices')
	const [targetForm, setTargetForm] = useState<{ open: boolean, target: Target | null }>({ open: false, target: null })
	const [providerForm, setProviderForm] = useState<{ open: boolean, provider: Provider | null }>({ open: false, provider: null })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set())
  const [live, setLive] = useState(false)

  const refresh = useCallback(async (signal?: AbortSignal) => {
    try {
	  const [deviceData, targetData, providerData] = await Promise.all([listDevices(signal), listTargets(signal), listProviders(signal)])
	  setDevices(deviceData)
	  setTargets(targetData)
	  setProviders(providerData)
      setError(null)
    } catch (cause) {
      if (cause instanceof DOMException && cause.name === 'AbortError') return
      setError(cause instanceof Error ? cause.message : '无法连接后端')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void refresh(controller.signal)
    const timer = window.setInterval(() => void refresh(), 30000)
    const unsubscribe = subscribeDevices((updated) => setDevices((current) => { const exists = current.some((item) => item.id === updated.id); return exists ? current.map((item) => item.id === updated.id ? updated : item) : [...current, updated] }), setLive)
    return () => {
      controller.abort()
      window.clearInterval(timer)
      unsubscribe()
    }
  }, [refresh])

  async function handlePowerChange(device: Device, value: boolean) {
    setPendingIds((current) => new Set(current).add(device.id))
    try {
      const updated = await setDevicePower(device.id, value)
      setDevices((current) => current.map((item) => item.id === updated.id ? updated : item))
      setError(null)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '控制设备失败')
    } finally {
      setPendingIds((current) => {
        const next = new Set(current)
        next.delete(device.id)
        return next
      })
    }
  }

	async function handleTargetSave(input: TargetInput, editing: boolean) {
		try {
			await saveTarget(input, editing)
			setTargetForm({ open: false, target: null })
			await refresh()
			setError(null)
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '保存桥失败')
			throw cause
		}
	}

	async function handleTargetDelete(target: Target) {
		if (!window.confirm(`确定删除“${target.name}”吗？配对资料目录不会自动删除。`)) return
		try { await deleteTarget(target.id); await refresh() } catch (cause) { setError(cause instanceof Error ? cause.message : '删除桥失败') }
	}
	async function handleProviderSave(input: ProviderInput, editing: boolean) { await saveProvider(input, editing); setProviderForm({ open: false, provider: null }); await refresh() }
	async function handleProviderDelete(provider: Provider) { if (!window.confirm(`确定删除“${provider.name}”吗？其设备将立即离线。`)) return; try { await deleteProvider(provider.id); await refresh() } catch (cause) { setError(cause instanceof Error ? cause.message : '删除 Provider 失败') } }
	async function handleSimulation(device: Device, values: { online?: boolean; power?: boolean; temperature?: number }) { try { const updated = await simulateDevice(device.id, values); setDevices((current) => current.map((item) => item.id === updated.id ? updated : item)); setError(null) } catch (cause) { setError(cause instanceof Error ? cause.message : '模拟状态失败'); throw cause } }
	const pageCopy = page === 'devices' ? { title: <>把家的状态<br />织在一起。</>, intro: '设备状态驻留内存，由 Provider 实时上报。', eyebrow: 'DEVICES', section: '设备中心' } : page === 'providers' ? { title: <>数据源，随时<br />接入或离开。</>, intro: 'Provider 配置存储于 SQLite，并可在线启停和替换。', eyebrow: 'PROVIDERS', section: 'Provider 管理' } : { title: <>一座桥，或<br />很多座桥。</>, intro: '按设备或平台拆分桥实例。每座桥拥有独立身份、端口、配对资料和二维码。', eyebrow: 'TARGETS', section: '桥接中心' }
	const summary = page === 'devices' ? devices.filter((item) => item.online).length : page === 'providers' ? providers.filter((item) => item.status === 'running').length : targets.filter((item) => item.status === 'running').length

  return (
    <main>
	  <nav className="top-nav">
	    <a className="wordmark" href="#">HomeLoom</a>
	    <div>
	      <button className={page === 'devices' ? 'is-active' : ''} onClick={() => setPage('devices')}>设备</button>
	      <button className={page === 'providers' ? 'is-active' : ''} onClick={() => setPage('providers')}>Provider</button>
	      <button className={page === 'targets' ? 'is-active' : ''} onClick={() => setPage('targets')}>桥接中心</button>
	    </div>
	    <span className={`live-indicator ${live ? 'is-live' : ''}`}>{live ? '实时' : '重连中'}</span>
	  </nav>
      <header className="hero">
        <div>
          <p className="eyebrow">HOMELOOM · DEMO 01</p>
		  <h1>{pageCopy.title}</h1><p className="intro">{pageCopy.intro}</p>
        </div>
        <div className="summary">
		  <span>{summary}</span><small>{page === 'devices' ? '在线设备' : page === 'providers' ? '运行中 Provider' : '运行中的桥'}</small>
        </div>
      </header>

      <section className="section-heading">
        <div>
		  <p className="eyebrow">{pageCopy.eyebrow}</p><h2>{pageCopy.section}</h2>
        </div>
		<div className="heading-actions">{page === 'providers' && <button className="add-button" onClick={() => setProviderForm({ open: true, provider: null })}>＋ 新建 Provider</button>}{page === 'targets' && <button className="add-button" onClick={() => setTargetForm({ open: true, target: null })}>＋ 新建桥</button>}<button className="refresh-button" onClick={() => void refresh()} disabled={loading}>刷新状态</button></div>
      </section>

      {error && <div className="error-banner">{error}，请确认后端已在 8090 端口运行。</div>}
      {loading ? (
        <div className="empty-state">正在连接 HomeLoom…</div>
      ) : (
		page === 'devices' ? <section className="device-grid">
		  {devices.map((device) => (
            <DeviceCard
              key={device.id}
              device={device}
              pending={pendingIds.has(device.id)}
              onPowerChange={(item, value) => void handlePowerChange(item, value)}
            />
          ))}
		</section> : page === 'providers' ? <section className="provider-grid"><div className="config-note"><span>配置来源</span><strong>SQLite · providers</strong><p>保存后运行时立即应用；模拟状态仅驻留内存，重启后按配置重建。</p></div>{providers.map((provider) => <ProviderCard key={provider.id} provider={provider} devices={devices.filter((item) => item.providerId === provider.id)} onEdit={(item) => setProviderForm({ open: true, provider: item })} onDelete={(item) => void handleProviderDelete(item)} onSimulate={handleSimulation} />)}</section> : <section className="target-list">
		  <div className="config-note">
		    <span>配置来源</span>
		    <strong>SQLite · targets</strong>
		    <p>桥配置、设备绑定和配对参数统一保存在数据库中；YAML 只负责进程启动。</p>
		  </div>
		  {targets.map((target) => <TargetCard key={target.id} target={target} onEdit={(item) => setTargetForm({ open: true, target: item })} onDelete={(item) => void handleTargetDelete(item)} />)}
		</section>
      )}
	  {targetForm.open && <TargetForm target={targetForm.target} devices={devices} onCancel={() => setTargetForm({ open: false, target: null })} onSave={handleTargetSave} />}
	  {providerForm.open && <ProviderForm provider={providerForm.provider} onCancel={() => setProviderForm({ open: false, provider: null })} onSave={handleProviderSave} />}
    </main>
  )
}
