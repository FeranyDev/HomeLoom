import { useState } from 'react'
import { discoverXiaomiGateways, type XiaomiGateway } from '../api/xiaomi'
import type { Device } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { CollectionEmpty } from './PageState'

function objectValue(value: unknown): Record<string, unknown> {
	return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function arrayValue(value: unknown): unknown[] { return Array.isArray(value) ? value : [] }

function credentialReady(config: Record<string, unknown>): boolean {
	return Boolean(config.clientId && config.clientCertificate && config.privateKey)
}

export function XiaomiWorkspace({ providers, devices, onCreate, onEdit, onManageDevices, onDelete, onRestart, onTest }: {
	providers: Provider[]
	devices: Device[]
	onCreate: () => void
	onEdit: (provider: Provider) => void
	onManageDevices: (provider: Provider) => void
	onDelete: (provider: Provider) => void
	onRestart: (provider: Provider) => Promise<void>
	onTest: (provider: ProviderInput) => Promise<void>
}) {
	const [gateways, setGateways] = useState<XiaomiGateway[]>([])
	const [discovering, setDiscovering] = useState(false)
	const [discoveryError, setDiscoveryError] = useState<string | null>(null)
	const [busy, setBusy] = useState<string | null>(null)

	async function discover() {
		setDiscovering(true); setDiscoveryError(null)
		try { setGateways(await discoverXiaomiGateways()) } catch (cause) { setDiscoveryError(cause instanceof Error ? cause.message : '中枢发现失败') } finally { setDiscovering(false) }
	}

	async function run(provider: Provider, action: 'test' | 'restart') {
		setBusy(`${provider.id}:${action}`)
		try { if (action === 'test') await onTest(provider); else await onRestart(provider) } finally { setBusy(null) }
	}

	return <section className="xiaomi-workspace">
		<div className="xiaomi-overview">
			<div><p className="eyebrow">XIAOMI HOME</p><h3>先连接中枢，再管理设备。</h3><p>OAuth 与中枢连接、子设备映射分开管理；凭据和映射保存在 SQLite，实时状态由 MQTT 重建到内存。</p></div>
			<div className="xiaomi-overview__actions"><button className="add-button" onClick={onCreate}>＋ 接入米家</button><button onClick={() => void discover()} disabled={discovering}>{discovering ? '正在扫描局域网…' : '发现中枢网关'}</button></div>
		</div>

		<div className="xiaomi-flow" aria-label="米家接入流程">
			<div><span>01</span><strong>OAuth 与证书</strong><small>授权后申请 MQTT 客户端证书</small></div>
			<div><span>02</span><strong>连接中枢</strong><small>mDNS + MQTT 5 双向 TLS</small></div>
			<div><span>03</span><strong>获取子设备</strong><small>仅复用已运行的 MQTT 连接</small></div>
			<div><span>04</span><strong>映射与同步</strong><small>统一模型 + 实时状态订阅</small></div>
		</div>

		{discoveryError && <p className="inline-error" role="alert">{discoveryError}</p>}
		{gateways.length > 0 && <div className="xiaomi-gateways"><div className="command-heading"><h3>局域网中枢</h3><span>{gateways.length} 个候选</span></div>{gateways.map((gateway) => <article key={`${gateway.instance}-${gateway.hostName}`}><span className={`status-dot ${gateway.mqttEnabled ? 'is-online' : ''}`} /><div><strong>{gateway.instance}</strong><small>{gateway.addresses[0] ?? gateway.hostName}:{gateway.port}</small></div><div><b>{gateway.role === 1 ? '主中枢' : `角色 ${gateway.role ?? '未知'}`}</b><small>{gateway.mqttEnabled ? '本地 MQTT 可用' : '未声明 MQTT'}</small></div><code>{gateway.did ?? 'DID 未广播'}</code></article>)}</div>}

		{providers.length === 0 ? <CollectionEmpty title="还没有接入米家" description="点击“接入米家”，先完成账号授权、证书申请和中枢 MQTT 连接。" /> : <div className="xiaomi-provider-list">{providers.map((provider) => {
			const config = objectValue(provider.config)
			const oauth = objectValue(config.oauth)
			const mappings = arrayValue(config.devices)
			const ownedDevices = devices.filter((item) => item.providerId === provider.id && !item.removed)
			const onlineDevices = ownedDevices.filter((item) => item.availability === 'online').length
			const authorized = Boolean(oauth.clientId && oauth.oauthUuid && oauth.virtualDid)
			const certificate = credentialReady(config)
			return <article className="xiaomi-provider" key={provider.id}>
				<header><div><div className="device-card__topline"><span className={`status-dot ${provider.status === 'running' ? 'is-online' : ''}`} />{provider.status}<span className="provider">{provider.id}</span></div><h3>{provider.name}</h3><p>{String(config.host || '尚未选择中枢')}:{Number(config.port || 8883)}</p></div><div className="xiaomi-provider__actions"><button onClick={() => onEdit(provider)}>账号与中枢</button><button disabled={provider.status !== 'running'} title={provider.status === 'running' ? '通过当前 MQTT 连接读取和配置子设备' : '请先连接中枢 MQTT'} onClick={() => onManageDevices(provider)}>管理子设备</button><button disabled={busy !== null || !certificate} onClick={() => void run(provider, 'test')}>{busy === `${provider.id}:test` ? '测试中…' : '测试连接'}</button><button disabled={busy !== null || !provider.enabled} onClick={() => void run(provider, 'restart')}>{busy === `${provider.id}:restart` ? '重连中…' : '重新连接'}</button><button className="is-danger" onClick={() => onDelete(provider)}>删除</button></div></header>
				<div className="xiaomi-status-grid">
					<div><span>账号授权</span><strong className={authorized ? 'is-ready' : ''}>{authorized ? '已建立' : '待授权'}</strong><small>{String(oauth.region || 'cn').toUpperCase()} · UID {String(oauth.uid || '—')}</small></div>
					<div><span>中枢证书</span><strong className={certificate ? 'is-ready' : ''}>{certificate ? '已就绪' : '待申请'}</strong><small>Virtual DID {String(config.clientId || oauth.virtualDid || '—')}</small></div>
					<div><span>设备映射</span><strong>{mappings.length}</strong><small>数据库期望配置</small></div>
					<div><span>实时设备</span><strong>{onlineDevices} / {ownedDevices.length}</strong><small>当前内存快照</small></div>
				</div>
				<div className="xiaomi-runtime"><span>请求 <b>{provider.metrics?.requests ?? 0}</b></span><span>事件 <b>{provider.metrics?.events ?? 0}</b></span><span>错误 <b>{provider.metrics?.errors ?? 0}</b></span><span>轮询 <b>{Number(config.pollIntervalSeconds || 60)}s</b></span></div>
				{provider.error && <div className="provider-error"><p className="inline-error">{provider.error}</p><small>自动重试 {provider.retryCount ?? 0} 次{provider.nextRetryAt ? ` · 下次 ${new Date(provider.nextRetryAt).toLocaleTimeString()}` : ''}</small></div>}
				<div className="xiaomi-device-list"><div className="command-heading"><h3>已发布设备</h3><span>{ownedDevices.length} 台</span></div>{ownedDevices.length === 0 ? <p>连接中枢并完成映射后，设备会在这里显示。</p> : ownedDevices.map((item) => <div key={item.id}><span className={`status-dot is-${item.availability}`} /><strong>{item.name}</strong><code>{item.id}</code><small>{item.type} · seq {item.sequence ?? '—'}</small></div>)}</div>
			</article>
		})}</div>}
	</section>
}
