import { useCallback, useEffect, useState } from 'react'
import { listDevices, setDevicePower } from './api/devices'
import { deleteTarget, listTargets, saveTarget } from './api/targets'
import { DeviceCard } from './components/DeviceCard'
import { TargetCard } from './components/TargetCard'
import { TargetForm } from './components/TargetForm'
import type { Device } from './types/device'
import type { Target, TargetInput } from './types/target'

type Page = 'devices' | 'targets'

export function App() {
  const [devices, setDevices] = useState<Device[]>([])
	const [targets, setTargets] = useState<Target[]>([])
	const [page, setPage] = useState<Page>('devices')
	const [targetForm, setTargetForm] = useState<{ open: boolean, target: Target | null }>({ open: false, target: null })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [pendingIds, setPendingIds] = useState<Set<string>>(new Set())

  const refresh = useCallback(async (signal?: AbortSignal) => {
    try {
	  const [deviceData, targetData] = await Promise.all([listDevices(signal), listTargets(signal)])
	  setDevices(deviceData)
	  setTargets(targetData)
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
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => {
      controller.abort()
      window.clearInterval(timer)
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

  return (
    <main>
	  <nav className="top-nav">
	    <a className="wordmark" href="#">HomeLoom</a>
	    <div>
	      <button className={page === 'devices' ? 'is-active' : ''} onClick={() => setPage('devices')}>设备</button>
	      <button className={page === 'targets' ? 'is-active' : ''} onClick={() => setPage('targets')}>桥接中心</button>
	    </div>
	  </nav>
      <header className="hero">
        <div>
          <p className="eyebrow">HOMELOOM · DEMO 01</p>
		  <h1>{page === 'devices' ? <>把家的状态<br />织在一起。</> : <>一座桥，或<br />很多座桥。</>}</h1>
		  <p className="intro">{page === 'devices' ? '第一条设备链路已经接通：Virtual Provider → Go Core → Web Console。' : '按设备或平台拆分桥实例。每座桥拥有独立身份、端口、配对资料和二维码。'}</p>
        </div>
        <div className="summary">
		  <span>{page === 'devices' ? devices.filter((device) => device.online).length : targets.filter((target) => target.status === 'running').length}</span>
		  <small>{page === 'devices' ? '在线设备' : '运行中的桥'}</small>
        </div>
      </header>

      <section className="section-heading">
        <div>
		  <p className="eyebrow">{page === 'devices' ? 'DEVICES' : 'TARGETS'}</p>
		  <h2>{page === 'devices' ? '虚拟家庭' : '桥接中心'}</h2>
        </div>
		<div className="heading-actions">{page === 'targets' && <button className="add-button" onClick={() => setTargetForm({ open: true, target: null })}>＋ 新建桥</button>}<button className="refresh-button" onClick={() => void refresh()} disabled={loading}>刷新状态</button></div>
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
		</section> : <section className="target-list">
		  <div className="config-note">
		    <span>配置来源</span>
		    <strong>SQLite · targets</strong>
		    <p>桥配置、设备绑定和配对参数统一保存在数据库中；YAML 只负责进程启动。</p>
		  </div>
		  {targets.map((target) => <TargetCard key={target.id} target={target} onEdit={(item) => setTargetForm({ open: true, target: item })} onDelete={(item) => void handleTargetDelete(item)} />)}
		</section>
      )}
	  {targetForm.open && <TargetForm target={targetForm.target} devices={devices} onCancel={() => setTargetForm({ open: false, target: null })} onSave={handleTargetSave} />}
    </main>
  )
}
