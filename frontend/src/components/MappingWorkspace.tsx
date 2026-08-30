import { MappingPreview } from './MappingPreview'
import { ProfileManager } from './ProfileManager'
import { UnifiedModelManager } from './UnifiedModelManager'
import { useMappingSection } from '../routing'
import { useState } from 'react'
import { HelpTooltip } from './HelpTooltip'

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
		  <h2><HelpTooltip label="转换规则说明" content="统一维护类型、单位、枚举和数值范围的转换。保存后可供多台设备复用。">转换规则</HelpTooltip></h2>
          <div className="profile-tools-badges"><span>可视化编排</span><span>双向验证</span><span>保存即热更新</span></div>
        </div>
        <ol className="profile-tools-journey" aria-label="转换配置使用流程">
          <li><span>01</span><div><strong>识别差异</strong><small>比较来源与目标属性</small></div></li>
          <li><span>02</span><div><strong>编排规则</strong><small>组合转换并验证样本</small></div></li>
          <li><span>03</span><div><strong>绑定设备</strong><small>在设备映射中启用</small></div></li>
        </ol>
      </header>
      <aside className="profile-tools-boundary" aria-label="转换配置应用位置">
		<div className="profile-tools-boundary-copy"><span>应用位置</span><strong><HelpTooltip label="转换规则应用说明" content="转换规则不直接关联设备，只维护可复用的转换逻辑。">在映射中启用</HelpTooltip></strong></div>
        <div className="profile-tools-entry"><span>提供端 → 统一模型</span><strong>设备中心</strong><small>从设备卡片进入属性映射</small></div>
        <div className="profile-tools-entry"><span>统一模型 → 消费端</span><strong>桥接中心</strong><small>从虚拟设备进入属性映射</small></div>
      </aside>
      <ProfileManager onChanged={() => setProfileRevision((current) => current + 1)} />
      <section className="saved-profile-preview" aria-labelledby="saved-profile-preview-title">
        <header>
          <span className="profile-section-number">02</span>
		  <div><p className="eyebrow">已保存配置验证（SAVED PROFILE TEST）</p><h3 id="saved-profile-preview-title"><HelpTooltip label="规则验证说明" content="选择已保存版本，用样本检查正向和反向结果；草稿可在编辑器内验证。">验证已保存规则</HelpTooltip></h3></div>
          <i>已保存版本</i>
        </header>
        <MappingPreview profileRevision={profileRevision} />
      </section>
    </div>}
  </section>
}
