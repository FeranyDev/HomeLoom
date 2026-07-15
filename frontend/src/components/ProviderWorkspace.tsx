import { useState } from 'react'
import { discoverXiaomiGateways, type XiaomiGateway } from '../api/xiaomi'
import type { Device, DeviceAvailability } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { CollectionEmpty } from './PageState'
import { ProviderCard } from './ProviderCard'

type SimulationValues = { availability?: DeviceAvailability; online?: boolean; power?: boolean; value?: number; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; active?: boolean; speed?: number; mode?: string; filterLife?: number; filterChange?: boolean; position?: number; sequence?: number; repeat?: number }

export function ProviderWorkspace({ providers, devices, onEdit, onManageDevices, onDelete, onRestart, onTest, onSimulate }: {
	providers: Provider[]
	devices: Device[]
	onEdit: (provider: Provider) => void
	onManageDevices: (provider: Provider) => void
	onDelete: (provider: Provider) => void
	onRestart: (provider: Provider) => Promise<void>
	onTest: (provider: ProviderInput) => Promise<void>
	onSimulate: (device: Device, values: SimulationValues) => Promise<void>
}) {
	const [gateways, setGateways] = useState<XiaomiGateway[]>([])
	const [discovering, setDiscovering] = useState(false)
	const [discoveryError, setDiscoveryError] = useState<string | null>(null)

	async function discover() {
		setDiscovering(true); setDiscoveryError(null)
		try { setGateways(await discoverXiaomiGateways()) } catch (cause) { setDiscoveryError(cause instanceof Error ? cause.message : '中枢发现失败') } finally { setDiscovering(false) }
	}

	return <section className="provider-workspace">
		<div className="provider-overview">
			<div><p className="eyebrow">PROVIDER RUNTIME</p><h3>一种生命周期，接入所有数据源。</h3><p>三种 Provider 都按配置、初始化连接、发现设备和实时发布运行；配置保存在 SQLite，设备状态在内存中重建。</p></div>
			<button onClick={() => void discover()} disabled={discovering}>{discovering ? '正在扫描局域网…' : '发现小米中枢网关'}</button>
		</div>
		<div className="provider-flow" aria-label="Provider 运行流程">
			<div><span>01</span><strong>配置与凭据</strong><small>数据库保存，敏感字段脱敏</small></div>
			<div><span>02</span><strong>初始化连接</strong><small>实例独立运行、失败和重试</small></div>
			<div><span>03</span><strong>发现与配置设备</strong><small>从当前运行连接读取来源目录</small></div>
			<div><span>04</span><strong>发布与同步</strong><small>统一模型 + 实时内存状态</small></div>
		</div>
		{discoveryError && <p className="inline-error" role="alert">{discoveryError}</p>}
		{gateways.length > 0 && <div className="xiaomi-gateways"><div className="command-heading"><h3>局域网小米中枢</h3><span>{gateways.length} 个候选</span></div>{gateways.map((gateway) => <article key={`${gateway.instance}-${gateway.hostName}`}><span className={`status-dot ${gateway.mqttEnabled ? 'is-online' : ''}`} /><div><strong>{gateway.instance}</strong><small>{gateway.addresses[0] ?? gateway.hostName}:{gateway.port}</small></div><div><b>{gateway.role === 1 ? '主中枢' : `角色 ${gateway.role ?? '未知'}`}</b><small>{gateway.mqttEnabled ? '本地 MQTT 可用' : '未声明 MQTT'}</small></div><code>{gateway.did ?? 'DID 未广播'}</code></article>)}</div>}
		<div className="config-note"><span>配置来源</span><strong>SQLite · providers</strong><p>每个实例独立连接、发现、重试和发布；更新配置后立即替换对应运行实例。</p></div>
		{providers.length === 0 ? <CollectionEmpty title="还没有 Provider" description="新建 Virtual、MQTT 或 Xiaomi Provider 后，实例会立即初始化并进入统一运行流程。" /> : <div className="provider-list">{providers.map((provider) => <ProviderCard key={provider.id} provider={provider} devices={devices.filter((item) => item.providerId === provider.id && !item.removed)} onEdit={onEdit} onManageDevices={onManageDevices} onDelete={onDelete} onRestart={onRestart} onTest={onTest} onSimulate={onSimulate} />)}</div>}
	</section>
}
