import { useState } from 'react'
import { MappingPreview } from './MappingPreview'
import { ProfileManager } from './ProfileManager'
import { UnifiedModelManager } from './UnifiedModelManager'

export function MappingWorkspace() {
  const [profileRevision, setProfileRevision] = useState(0)
  const [section, setSection] = useState<'models' | 'profiles'>('models')
  return <section className="mapping-page">
    <div className="mapping-page-tabs" role="tablist" aria-label="统一模型配置页面">
      <button className={section === 'models' ? 'is-active' : ''} aria-selected={section === 'models'} role="tab" onClick={() => setSection('models')}><span>统一模型</span><small>设备类型与三级属性字段</small></button>
      <button className={section === 'profiles' ? 'is-active' : ''} aria-selected={section === 'profiles'} role="tab" onClick={() => setSection('profiles')}><span>转换配置</span><small>跨类型转换 Profile 与预览</small></button>
    </div>
    {section === 'models' ? <UnifiedModelManager /> : <div className="mapping-profile-tools"><div className="config-note"><span>设备级映射入口</span><strong>提供端在设备中心配置 · 消费端在桥接中心配置</strong><p>这里仅维护可复用的转换配置（Profile）。具体设备的两段映射关系不会聚集到本页面。</p></div><ProfileManager onChanged={() => setProfileRevision((current) => current + 1)} /><MappingPreview profileRevision={profileRevision} /></div>}
  </section>
}
