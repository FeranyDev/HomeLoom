import { pairingQRUrl } from '../api/targets'
import type { Target } from '../types/target'

interface TargetCardProps {
  target: Target
	onEdit: (target: Target) => void
	onDelete: (target: Target) => void
}

const statusLabels = {
  disabled: '未启用',
  starting: '启动中',
  running: '运行中',
  error: '异常',
}

export function TargetCard({ target, onEdit, onDelete }: TargetCardProps) {
	const [showQR, setShowQR] = useState(false)
  const canPair = target.type === 'apple-hap' && target.enabled && Boolean(target.setupUri)

  return (
    <article className="target-card">
      <div className="target-card__content">
        <div className="device-card__topline">
          <span className={`status-dot ${target.status === 'running' ? 'is-online' : ''}`} />
          <span>{statusLabels[target.status]}</span>
          <span className="provider">{target.type}</span>
        </div>
        <p className="target-id">{target.id}</p>
        <h2>{target.name}</h2>
        <dl className="target-details">
          <div><dt>监听地址</dt><dd>{target.address || '—'}</dd></div>
          <div><dt>设备范围</dt><dd>{target.deviceIds.length ? `${target.deviceIds.length} 台指定设备` : '全部设备'}</dd></div>
          <div><dt>配对码</dt><dd className="pairing-code">{target.pairingCode || '—'}</dd></div>
        </dl>
		<div className="target-actions">
		  <button onClick={() => onEdit(target)}>编辑配置</button>
		  <button className="is-danger" onClick={() => onDelete(target)}>删除</button>
		</div>
      </div>

      <div className={`pairing-panel ${canPair ? '' : 'is-unavailable'}`}>
		{canPair && showQR ? (
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
            <strong>{target.type === 'matter' ? '类型尚未实现' : '启用后生成二维码'}</strong>
            <span>每个桥拥有独立的配对资料</span>
          </>
        )}
      </div>
    </article>
  )
}
import { useState } from 'react'
