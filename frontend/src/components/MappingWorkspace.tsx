import { useState } from 'react'
import { MappingPreview } from './MappingPreview'
import { ProfileManager } from './ProfileManager'
import { CustomModelPropertyManager } from './CustomModelPropertyManager'

export function MappingWorkspace() {
  const [profileRevision, setProfileRevision] = useState(0)
  const [catalogRevision, setCatalogRevision] = useState(0)
  return <section className="mapping-page"><div className="config-note"><span>设备映射入口</span><strong>设备中心 · 对应设备 · 配置映射</strong><p>设备路由不在这里集中展示。请从具体设备进入，两段映射会锁定该设备；本页只维护可复用的统一模型定义和转换工具。</p></div><CustomModelPropertyManager onChanged={() => setCatalogRevision((current) => current + 1)} key={catalogRevision} /><ProfileManager onChanged={() => setProfileRevision((current) => current + 1)} /><MappingPreview profileRevision={profileRevision} /></section>
}
