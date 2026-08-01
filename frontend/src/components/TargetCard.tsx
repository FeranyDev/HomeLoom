import { useState } from 'react'
import { matterQRUrl, pairingQRUrl } from '../api/targets'
import { isMatterTarget } from '../types/target'
import type { MatterFabric, MatterTarget, Target } from '../types/target'
import type { Device } from '../types/device'
import { availabilityLabel } from '../types/device'
import { targetTypeLabel } from '../presentationLabels'
import { targetDescriptor } from '../targetDescriptors'

interface TargetCardProps {
	target: Target
	onEdit: (target: Target) => void
	onDelete: (target: Target) => void
	onRegeneratePairing: (target: Target) => void
	onClearPairingIdentity: (target: Target) => void
	onManageDevices: (target: Target) => void
	sourceDevice?: Device
	onPreviewCamera?: (device: Device) => void
	onMatterCommissioningToggle?: (target: MatterTarget, open: boolean) => void
	onDeleteMatterFabric?: (target: MatterTarget, fabric: MatterFabric) => void
	onFactoryResetMatter?: (target: MatterTarget) => void
	onEnabledChange?: (target: MatterTarget, enabled: boolean) => void
}

const statusLabels = { disabled: '未启用', starting: '启动中', running: '运行中', error: '异常' }
const commissioningLabels = { uncommissioned: '尚未加入 Fabric', 'window-open': '配网窗口已打开', commissioned: '已加入 Fabric', unknown: '等待运行时状态' }

export function TargetCard({ target, onEdit, onDelete, onRegeneratePairing, onClearPairingIdentity, onManageDevices, sourceDevice, onPreviewCamera, onMatterCommissioningToggle, onDeleteMatterFabric, onFactoryResetMatter, onEnabledChange }: TargetCardProps) {
	const [showQR, setShowQR] = useState(false)
	const descriptor = targetDescriptor(target.type)
	const consumerId = target.consumerId ?? descriptor.consumerId
	const homeKit = target.type === 'apple-hap' || target.type === 'homekit-camera'
	const matterTarget = isMatterTarget(target) ? target : null
	const camera = target.type === 'homekit-camera' || target.type === 'matter-camera'
	const canPair = homeKit && !target.pairing.paired && target.enabled && Boolean(target.pairing.setupUri || target.pairing.pairingCode)
	const canShowQR = canPair && Boolean(target.pairing.setupUri)
	const matterCanPair = matterTarget !== null && matterTarget.enabled && matterTarget.commissioning.windowOpen

	return <article className="target-card">
		<div className="target-card__content">
			<div className="device-card__topline"><span className={`status-dot ${target.status === 'running' ? 'is-online' : ''}`} /><span>{statusLabels[target.status]}</span><span className="provider">{targetTypeLabel(target.type)}</span></div>
			<p className="target-id">{target.id}</p><h2>{target.name}</h2>
			<dl className="target-details">
				<div><dt>消费适配器</dt><dd>{descriptor.consumerName}（{consumerId}）</dd></div>
				<div><dt>消费端设备</dt><dd>{target.devices.length || target.deviceIds.length} 台</dd></div>
				{homeKit ? <>
					<div><dt>HAP 监听地址</dt><dd>{target.config.address || '自动分配'}</dd></div>
					<div><dt>HomeKit 状态</dt><dd>{target.pairing.paired ? '已配对至 Apple Home' : '等待配对'}</dd></div>
					{target.type === 'homekit-camera' && <><div><dt>摄像头来源</dt><dd>{sourceDevice ? `${sourceDevice.name} · ${sourceDevice.providerId}` : target.devices[0]?.sourceDeviceId ?? target.deviceIds[0] ?? '来源不可用'}</dd></div><div><dt>摄像头状态</dt><dd>{sourceDevice ? availabilityLabel(sourceDevice.availability) : '设备中心未找到'}</dd></div></>}
					<div><dt>运行时</dt><dd>{statusLabels[target.status]}{target.error ? ` · ${target.error}` : ''}</dd></div>
				</> : <>
					<div><dt>运行时</dt><dd>{statusLabels[target.status]}{target.error ? ` · ${target.error}` : ''}</dd></div>
					{target.type === 'matter-camera' && <><div><dt>摄像头来源</dt><dd>{sourceDevice ? `${sourceDevice.name} · ${sourceDevice.providerId}` : target.devices[0]?.sourceDeviceId ?? target.deviceIds[0] ?? '来源不可用'}</dd></div><div><dt>摄像头状态</dt><dd>{sourceDevice ? availabilityLabel(sourceDevice.availability) : '设备中心未找到'}</dd></div><div><dt>兼容性</dt><dd><span className="target-security-label">实验性 Controller 兼容 · 不保证 Apple Home</span></dd></div></>}
					<div><dt>配网状态</dt><dd>{commissioningLabels[target.commissioning.state]}{target.commissioning.windowExpiresAt ? ` · 截止 ${new Date(target.commissioning.windowExpiresAt).toLocaleString('zh-CN')}` : ''}</dd></div>
					<div><dt>Fabric / Endpoint</dt><dd>{target.fabricCount} 个 Fabric · {target.endpointCount} 个 Endpoint</dd></div>
					<div><dt>网络接口</dt><dd>{target.runtime?.interface ?? target.config.networkInterface ?? '自动选择'} · UDP {target.config.udpPort ?? '自动'}</dd></div>
					<div><dt>协议版本</dt><dd>{target.runtime?.protocolVersion ?? target.config.protocolVersion ?? '运行时协商中'}</dd></div>
					<div><dt>认证状态</dt><dd><span className={target.certification === 'certified' ? 'target-security-label is-certified' : 'target-security-label'}>{target.certification === 'certified' ? '已认证设备' : '测试设备 · 未认证'}</span></dd></div>
				</>}
			</dl>
			{(target.error || (target.issues && target.issues.length > 0)) && <div className="provider-error target-error" role="alert">
				{target.error && <p className="inline-error">{target.error}</p>}
				{target.diagnostics?.skippedAccessories && <small>已跳过配件 {target.diagnostics.skippedAccessories}{target.diagnostics.publishedAccessories ? ` · 已发布 ${target.diagnostics.publishedAccessories}` : ''}</small>}
				{target.issues && target.issues.length > 0 && <ul className="target-issue-list">
					{target.issues.map((issue, index) => {
						const label = issue.deviceName || issue.deviceId || issue.deviceType || `问题 ${index + 1}`
						return <li key={`${issue.deviceId ?? 'device'}-${issue.stage}-${index}`}><strong>{label}</strong><span>{issue.stage}</span><code>{issue.message}</code></li>
					})}
				</ul>}
			</div>}
			<div className="target-actions">
				<button onClick={() => onEdit(target)}>编辑配置</button>{!camera && <button onClick={() => onManageDevices(target)}>配置消费端设备</button>}{camera && sourceDevice && <button onClick={() => onPreviewCamera?.(sourceDevice)}>设备中心实时预览</button>}
				{homeKit && !target.pairing.paired && target.type === 'apple-hap' && <button onClick={() => onRegeneratePairing(target)}>重新生成 HomeKit 配对参数</button>}
				{homeKit && target.pairing.paired && <button className="is-danger" onClick={() => onClearPairingIdentity(target)}>清除 HomeKit 配对身份</button>}
				{matterTarget?.type === 'matter-camera' && <button onClick={() => onEnabledChange?.(matterTarget, !matterTarget.enabled)}>{matterTarget.enabled ? '停用发布' : '启用发布'}</button>}
				{matterTarget && (matterTarget.commissioning.windowOpen
					? <button onClick={() => onMatterCommissioningToggle?.(matterTarget, false)}>关闭配网窗口</button>
					: <button onClick={() => onMatterCommissioningToggle?.(matterTarget, true)}>打开配网窗口</button>)}
				{matterTarget?.fabrics?.map((fabric) => <button key={fabric.id} className="is-danger" onClick={() => onDeleteMatterFabric?.(matterTarget, fabric)}>删除 Fabric {fabric.label ?? fabric.id}</button>)}
				{matterTarget && <button className="is-danger" onClick={() => onFactoryResetMatter?.(matterTarget)}>恢复 Matter 出厂身份</button>}
				<button className="is-danger" onClick={() => onDelete(target)}>删除</button>
			</div>
		</div>

		{homeKit ? <div className={`pairing-panel ${canPair || target.pairing.paired ? '' : 'is-unavailable'} ${target.pairing.paired ? 'is-paired' : ''}`}>
			{target.pairing.paired ? <><div className="paired-mark" aria-hidden="true">✓</div><strong>已连接 Apple Home</strong><span>配对完成后，PIN、Setup ID 和二维码不再参与日常运行。</span></>
				: canShowQR && showQR ? <><img src={pairingQRUrl(target.id)} alt={`${target.name} HomeKit 配对二维码`} /><strong>使用“家庭”App 扫描</strong><span>二维码与此桥的 Setup ID、类型及配对码绑定</span><span className="pairing-pin">备用 PIN：{target.pairing.pairingCode}</span><button className="hide-qr" onClick={() => setShowQR(false)}>隐藏二维码</button></>
					: canShowQR ? <><div className="qr-placeholder">⌁</div><strong>配对信息默认隐藏</strong><span>仅在准备绑定 Apple Home 时显示二维码</span><button className="show-qr" onClick={() => setShowQR(true)}>显示配对二维码</button></>
						: canPair ? <><div className="qr-placeholder">⌁</div><strong>在 Apple Home 中选择“更多选项”</strong><span>手动配对码：{target.pairing.pairingCode}</span><small>选择名称为“{target.name}”的独立摄像头配件。</small></>
						: <><div className="qr-placeholder">{target.type === 'homekit-camera' ? 'CAM' : 'QR'}</div><strong>{target.type === 'homekit-camera' ? '等待独立摄像头发布器' : '启用后生成 HomeKit 二维码'}</strong><span>{target.type === 'homekit-camera' ? '发布器启动后会显示扫码二维码，并保留手动配对码作为备用' : '每个 Apple HAP 目标拥有独立配对资料'}</span></>}
		</div> : <div className={`pairing-panel matter-pairing-panel ${matterCanPair ? '' : 'is-unavailable'}`}>
			{matterCanPair ? <><img src={matterQRUrl(target.id)} alt={`${target.name} Matter 配网二维码`} /><strong>Matter 配网窗口已打开</strong><span>{target.commissioning.manualPairingCode ? `手工配对码：${target.commissioning.manualPairingCode}` : '请使用 Matter 控制器扫描二维码'}</span><small>窗口关闭后二维码与配对码会立即隐藏。</small></>
				: <><div className="qr-placeholder">{target.type === 'matter-camera' ? 'MC' : 'M'}</div><strong>配网二维码已隐藏</strong><span>{target.fabricCount > 0 ? '已加入 Fabric；如需新增控制器，请临时打开配网窗口。' : '打开配网窗口后才会生成并显示二维码。'}</span><small>{target.type === 'matter-camera' ? '实验性 Matter Camera · 不使用 HomeKit PIN' : '测试设备 · 未认证'}</small></>}
		</div>}
	</article>
}
