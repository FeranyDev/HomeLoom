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
	return <section className="provider-workspace">
		<div className="provider-overview">
			<div><p className="eyebrow">PROVIDER RUNTIME</p><h3>一种生命周期，接入所有数据源。</h3><p>Provider 只管理连接与凭据；MQTT 客户端、MQTT 服务端、小米中枢和第三方兼容 MIoT 云均作为独立入口创建，设备在连接成功后单独配置。</p></div>
		</div>
		<div className="provider-flow" aria-label="Provider 运行流程">
			<div><span>01</span><strong>配置与凭据</strong><small>数据库保存，敏感字段脱敏</small></div>
			<div><span>02</span><strong>初始化连接</strong><small>实例独立运行、失败和重试</small></div>
			<div><span>03</span><strong>发现与配置设备</strong><small>从当前运行连接读取来源目录</small></div>
			<div><span>04</span><strong>发布与同步</strong><small>统一模型 + 实时内存状态</small></div>
		</div>
		<div className="config-note"><span>配置来源</span><strong>PostgreSQL · providers</strong><p>每个实例独立连接、发现、重试和发布；更新配置后立即替换对应运行实例。</p></div>
		{providers.length === 0 ? <CollectionEmpty title="还没有 Provider" description="新建 Virtual、MQTT 客户端、MQTT 服务端、小米中枢或第三方兼容 MIoT 云 Provider 后，实例会立即初始化并进入统一运行流程。" /> : <div className="provider-list">{providers.map((provider) => <ProviderCard key={provider.id} provider={provider} devices={devices.filter((item) => item.providerId === provider.id && !item.removed)} onEdit={onEdit} onManageDevices={onManageDevices} onDelete={onDelete} onRestart={onRestart} onTest={onTest} onSimulate={onSimulate} />)}</div>}
	</section>
}
