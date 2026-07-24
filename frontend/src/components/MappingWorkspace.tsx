import { MappingPreview } from './MappingPreview'
import { ProfileManager } from './ProfileManager'
import { UnifiedModelManager } from './UnifiedModelManager'
import { useMappingSection } from '../routing'
import { useState } from 'react'

export function MappingWorkspace() {
  const [profileRevision, setProfileRevision] = useState(0)
  const [section, setSection] = useMappingSection()
  return <section className="mapping-page">
    <div className="mapping-page-tabs" role="tablist" aria-label="统一模型配置页面">
      <button className={section === 'models' ? 'is-active' : ''} aria-selected={section === 'models'} role="tab" onClick={() => setSection('models')}><span>统一模型</span><small>设备类型与三级属性字段</small></button>
      <button className={section === 'profiles' ? 'is-active' : ''} aria-selected={section === 'profiles'} role="tab" onClick={() => setSection('profiles')}><span>转换配置</span><small>跨类型转换 Profile 与预览</small></button>
    </div>
    {section === 'models' ? <UnifiedModelManager /> : <div className="mapping-profile-tools">
      <header className="profile-tools-hero" aria-label="转换配置工作台">
        <div className="profile-tools-intro">
          <p className="eyebrow">转换配置工作台（PROFILE WORKBENCH）</p>
          <h2>把属性差异整理成可复用规则。</h2>
          <p>统一维护类型、单位、枚举与数值范围转换。每个 Profile 可以被多台设备复用，并在保存后立即进入映射目录。</p>
          <div className="profile-tools-badges"><span>可视化编排</span><span>双向验证</span><span>保存即热更新</span></div>
        </div>
        <ol className="profile-tools-journey" aria-label="转换配置使用流程">
          <li><span>01</span><div><strong>识别差异</strong><small>比较来源与目标属性</small></div></li>
          <li><span>02</span><div><strong>编排规则</strong><small>组合转换并验证样本</small></div></li>
          <li><span>03</span><div><strong>绑定设备</strong><small>在设备映射中启用</small></div></li>
        </ol>
      </header>
      <aside className="profile-tools-boundary" aria-label="转换配置应用位置">
        <div className="profile-tools-boundary-copy"><span>应用边界</span><strong>规则在这里创建，在设备映射中启用</strong><p>转换配置不直接选择设备，只维护跨设备复用的转换逻辑。</p></div>
        <div className="profile-tools-entry"><span>提供端 → 统一模型</span><strong>设备中心</strong><small>从设备卡片进入属性映射</small></div>
        <div className="profile-tools-entry"><span>统一模型 → 消费端</span><strong>桥接中心</strong><small>从虚拟设备进入属性映射</small></div>
      </aside>
      <ProfileManager onChanged={() => setProfileRevision((current) => current + 1)} />
      <section className="saved-profile-preview" aria-labelledby="saved-profile-preview-title">
        <header>
          <span className="profile-section-number">02</span>
          <div><p className="eyebrow">已保存配置验证（SAVED PROFILE TEST）</p><h3 id="saved-profile-preview-title">验证数据库中的 Profile</h3><p>选择已保存版本，用真实样本检查正向与反向结果；草稿验证仍在可视化编辑器内完成。</p></div>
          <i>已保存版本</i>
        </header>
        <MappingPreview profileRevision={profileRevision} />
      </section>
    </div>}
  </section>
}
