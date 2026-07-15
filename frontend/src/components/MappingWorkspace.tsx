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
    {section === 'models' ? <UnifiedModelManager /> : <div className="mapping-profile-tools"><div className="profile-flow-guide"><div><span>01</span><strong>识别属性差异</strong><small>比较来源、统一模型与消费端的数据类型和单位</small></div><i>→</i><div><span>02</span><strong>创建转换 Profile</strong><small>可视化编排转换链，并使用真实样本验证</small></div><i>→</i><div><span>03</span><strong>绑定到设备属性</strong><small>在设备中心或桥接中心选择已保存的 Profile</small></div></div><div className="config-note"><span>设备级映射入口</span><strong>提供端在设备中心配置 · 消费端在桥接中心配置</strong><p>这里维护可复用的转换规则；具体设备的两段映射仍分别在对应设备上配置。</p></div><ProfileManager onChanged={() => setProfileRevision((current) => current + 1)} /><section className="saved-profile-preview"><header><p className="eyebrow">已保存配置验证（SAVED PROFILE TEST）</p><h3>快速验证数据库中的 Profile</h3><span>用于回归验证已保存版本；创建时的草稿预览已经集成在可视化编辑器内。</span></header><MappingPreview profileRevision={profileRevision} /></section></div>}
  </section>
}
