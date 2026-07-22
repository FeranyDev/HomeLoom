import { useState } from 'react'
import { pairingQRUrl } from '../api/targets'
import type { Target } from '../types/target'
import { targetTypeLabel } from '../presentationLabels'
import { targetDescriptor } from '../targetDescriptors'

interface TargetCardProps {
  target: Target
	onEdit: (target: Target) => void
	onDelete: (target: Target) => void
	onRegeneratePairing: (target: Target) => void
	onClearPairingIdentity: (target: Target) => void
	onManageDevices: (target: Target) => void
}

const statusLabels = {
  disabled: '未启用',
  starting: '启动中',
  running: '运行中',
  error: '异常',
}

export function TargetCard({ target, onEdit, onDelete, onRegeneratePairing, onClearPairingIdentity, onManageDevices }: TargetCardProps) {
	const [showQR, setShowQR] = useState(false)
  const descriptor = targetDescriptor(target.type)
  const consumerId = target.consumerId ?? descriptor.consumerId
  const canPair = descriptor.supportsHomeKitPairing && !target.paired && target.enabled && Boolean(target.setupUri)

  return (
    <article className="target-card">
      <div className="target-card__content">
        <div className="device-card__topline">
          <span className={`status-dot ${target.status === 'running' ? 'is-online' : ''}`} />
          <span>{statusLabels[target.status]}</span>
          <span className="provider">{targetTypeLabel(target.type)}</span>
        </div>
        <p className="target-id">{target.id}</p>
        <h2>{target.name}</h2>
        <dl className="target-details">
          <div><dt>消费适配器</dt><dd>{descriptor.consumerName}（{consumerId}）</dd></div>
          <div><dt>消费端设备</dt><dd>{target.devices?.length ?? target.deviceIds.length} 台</dd></div>
          {descriptor.supportsHomeKitPairing && <><div><dt>HAP 监听地址</dt><dd>{target.address || '—'}</dd></div><div><dt>HomeKit 状态</dt><dd>{target.paired ? '已配对至 Apple Home' : '等待配对'}</dd></div></>}
        </dl>
		<div className="target-actions">
		  <button onClick={() => onEdit(target)}>编辑配置</button>
		  <button onClick={() => onManageDevices(target)}>配置消费端设备</button>
		  {descriptor.supportsHomeKitPairing && !target.paired && <button onClick={() => onRegeneratePairing(target)}>重新生成 HomeKit 配对参数</button>}
		  {descriptor.supportsHomeKitPairing && target.paired && <button className="is-danger" onClick={() => onClearPairingIdentity(target)}>清除 HomeKit 配对身份</button>}
		  <button className="is-danger" onClick={() => onDelete(target)}>删除</button>
		</div>
      </div>

	  {descriptor.supportsHomeKitPairing ? <div className={`pairing-panel ${canPair || target.paired ? '' : 'is-unavailable'} ${target.paired ? 'is-paired' : ''}`}>
		{target.paired ? (
		  <><div className="paired-mark" aria-hidden="true">✓</div><strong>已连接 Apple Home</strong><span>配对完成后，PIN、Setup ID 和二维码不再参与日常运行。</span></>
		) : canPair && showQR ? (
          <>
            <img src={pairingQRUrl(target.id)} alt={`${target.name} HomeKit 配对二维码`} />
            <strong>使用“家庭”App 扫描</strong>
            <span>二维码与此桥的 Setup ID、类型及配对码绑定</span>
			<button className="hide-qr" onClick={() => setShowQR(false)}>隐藏二维码</button>
          </>
		) : canPair ? (
		  <>
		    <div className="qr-placeholder">⌁</div>
		    <strong>配对信息默认隐藏</strong>
		    <span>仅在准备绑定 Apple Home 时显示二维码</span>
		    <button className="show-qr" onClick={() => setShowQR(true)}>显示配对二维码</button>
		  </>
        ) : (
          <>
            <div className="qr-placeholder">QR</div>
            <strong>启用后生成 HomeKit 二维码</strong>
            <span>每个 Apple HAP 目标拥有独立配对资料</span>
          </>
        )}
      </div> : <div className="pairing-panel is-unavailable target-adapter-panel"><div className="qr-placeholder">{descriptor.consumerId.slice(0, 1).toUpperCase()}</div><strong>{descriptor.consumerName} 消费端</strong><span>{descriptor.implemented ? '适配器已就绪' : '运行时与属性目录尚未实现，不会回退到 HomeKit'}</span></div>}
    </article>
  )
}
