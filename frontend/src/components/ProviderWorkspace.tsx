import type { Device, DeviceAvailability } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { CollectionEmpty } from './PageState'
import { ProviderCard } from './ProviderCard'
import { HelpTooltip } from './HelpTooltip'

type SimulationValues = { availability?: DeviceAvailability; online?: boolean; power?: boolean; value?: number; temperature?: number; humidity?: number; contact?: boolean; motion?: boolean; active?: boolean; speed?: number; mode?: string; filterLife?: number; filterChange?: boolean; position?: number; sequence?: number; repeat?: number }

export function ProviderWorkspace({ providers, devices, onEdit, onManageDevices, onDeviceLocation, onDelete, onRestart, onRevokeCredentials, onTest, onAuthChallengeComplete, onSimulate }: {
	providers: Provider[]
	devices: Device[]
	onEdit: (provider: Provider) => void
	onManageDevices: (provider: Provider) => void
	onDeviceLocation?: (device: Device) => void
	onDelete: (provider: Provider) => void
	onRestart: (provider: Provider) => Promise<void>
	onRevokeCredentials?: (provider: Provider) => Promise<void>
	onTest: (provider: ProviderInput) => Promise<void>
	onSimulate: (device: Device, values: SimulationValues) => Promise<void>
	onAuthChallengeComplete?: (provider: Provider) => Promise<void>
}) {
	return <section className="provider-workspace">
		<div className="provider-overview">
			<div><p className="eyebrow">PROVIDER RUNTIME</p><h3><HelpTooltip label="设备来源说明" content="每个来源独立连接、发现设备并同步状态。支持 Camera、MQTT、小米、MIoT 云、Tuya、Gree、网络设备和虚拟设备。">统一接入设备来源</HelpTooltip></h3></div>
		</div>
		<div className="provider-flow" aria-label="Provider 运行流程">
			<div><span>01</span><strong>配置与凭据</strong><small>数据库保存，敏感字段脱敏</small></div>
			<div><span>02</span><strong>初始化连接</strong><small>实例独立运行、失败和重试</small></div>
			<div><span>03</span><strong>发现与配置设备</strong><small>从当前运行连接读取来源目录</small></div>
			<div><span>04</span><strong>发布与同步</strong><small>统一模型 + 实时内存状态</small></div>
		</div>
		<div className="config-note"><span>配置</span><strong><HelpTooltip label="来源配置说明" content="保存后会用新配置替换该来源的运行实例。">来源实例</HelpTooltip></strong></div>
		{providers.length === 0 ? <CollectionEmpty title="还没有 Provider" description="新建 Camera、Virtual、MQTT、小米中枢、MIoT 云、Tuya、Gree 或网络设备 Provider 后，实例会立即初始化并进入统一运行流程。" /> : <div className="provider-list">{providers.map((provider) => <ProviderCard key={provider.id} provider={provider} devices={devices.filter((item) => item.providerId === provider.id && !item.removed)} onEdit={onEdit} onManageDevices={onManageDevices} onDeviceLocation={onDeviceLocation} onDelete={onDelete} onRestart={onRestart} onRevokeCredentials={onRevokeCredentials} onTest={onTest} onAuthChallengeComplete={onAuthChallengeComplete} onSimulate={onSimulate} />)}</div>}
	</section>
}
