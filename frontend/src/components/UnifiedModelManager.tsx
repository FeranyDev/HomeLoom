import { useCallback, useEffect, useMemo, useState } from 'react'
import { listModelContracts } from '../api/mapping'
import { bilingual, deviceTypeLabel, parameterLevelLabel, permissionLabel, propertyDisplayLabel, resourceLabel, unitLabel, valueTypeLabel } from '../presentationLabels'
import type { DeviceType, ParameterLevel } from '../types/device'
import type { ModelContract, ModelParameter } from '../types/mapping'
import { CustomModelPropertyManager } from './CustomModelPropertyManager'

type LevelFilter = 'all' | ParameterLevel

function behaviorLabel(value: string): string {
  const translated: Record<string, string> = {
    'must-publish': '必须发布', 'publish-if-supported': '支持时发布', 'preserve-and-mark-custom': '保留并标记为自定义',
    'must-map': '必须映射', 'map-if-supported': '支持时映射', 'explicit-path-mapping-only': '仅允许显式路径映射',
  }
  return bilingual(value, translated[value])
}

function countLevel(model: ModelContract, level: ParameterLevel): number {
  return model.parameters.filter((parameter) => parameter.level === level).length
}

function valueConstraint(parameter: ModelParameter): string {
  if (parameter.enum?.length) return `枚举（enum）：${parameter.enum.join(' / ')}`
  const parts = [parameter.min !== undefined && `最小值（min）${parameter.min}`, parameter.max !== undefined && `最大值（max）${parameter.max}`, parameter.step !== undefined && `步长（step）${parameter.step}`].filter(Boolean)
  return parts.length ? parts.join(' · ') : '无额外约束'
}

export function UnifiedModelManager() {
  const [models, setModels] = useState<ModelContract[]>([])
  const [selectedType, setSelectedType] = useState<DeviceType | null>(null)
  const [level, setLevel] = useState<LevelFilter>('all')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const result = await listModelContracts()
      setModels(result)
      setSelectedType((current) => current && result.some((model) => model.deviceType === current) ? current : result[0]?.deviceType ?? null)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '读取统一模型失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void refresh() }, [refresh])
  const selected = models.find((model) => model.deviceType === selectedType)
  const visibleParameters = useMemo(() => {
    if (!selected) return []
    const keyword = query.trim().toLowerCase()
    return selected.parameters.filter((parameter) => {
      if (level !== 'all' && parameter.level !== level) return false
      return !keyword || `${parameter.name} ${parameter.path.endpointId} ${parameter.path.capabilityId} ${parameter.path.propertyId} ${parameter.type} ${parameter.unit ?? ''}`.toLowerCase().includes(keyword)
    })
  }, [selected, level, query])
  const endpoints = useMemo(() => {
    const result = new Map<string, Map<string, ModelParameter[]>>()
    for (const parameter of visibleParameters) {
      const capabilities = result.get(parameter.path.endpointId) ?? new Map<string, ModelParameter[]>()
      const parameters = capabilities.get(parameter.path.capabilityId) ?? []
      parameters.push(parameter)
      capabilities.set(parameter.path.capabilityId, parameters)
      result.set(parameter.path.endpointId, capabilities)
    }
    return result
  }, [visibleParameters])

  if (loading) return <section className="model-catalog-loading" aria-label="统一模型加载中">正在读取统一模型…</section>
  if (error && models.length === 0) return <section className="model-catalog-loading"><p className="inline-error" role="alert">{error}</p><button onClick={() => void refresh()}>重新读取</button></section>

  return <section className="unified-model-manager">
    <header className="model-catalog-heading">
      <div><p className="eyebrow">统一智能设备模型（UNIFIED DEVICE MODELS）</p><h3>模型与属性字段配置</h3><p>统一模型是 Provider 与 Consumer 之间唯一的语义基准。内置的必需、可选字段保持只读；自定义字段保存到数据库并实时进入映射目录。</p></div>
      <div className="model-catalog-totals"><strong>{models.length}</strong><span>设备模型</span><strong>{models.reduce((total, model) => total + model.parameters.length, 0)}</strong><span>属性字段</span></div>
    </header>
    {error && <p className="inline-error" role="alert">{error}</p>}
    <div className="model-catalog-layout">
      <aside className="model-type-list" aria-label="统一设备模型列表">
        {models.map((model) => <button key={model.deviceType} className={selectedType === model.deviceType ? 'is-selected' : ''} onClick={() => { setSelectedType(model.deviceType); setLevel('all'); setQuery('') }}>
          <span>{deviceTypeLabel(model.deviceType)}</span><code>v{model.version}</code>
          <small><i className="is-required">{countLevel(model, 'required')}</i> 必需 · <i className="is-optional">{countLevel(model, 'optional')}</i> 可选 · <i className="is-custom">{countLevel(model, 'custom')}</i> 自定义</small>
        </button>)}
      </aside>
      {selected && <div className="model-contract-panel">
        <header className="model-contract-heading">
          <div><span>当前模型（deviceType）</span><h3>{deviceTypeLabel(selected.deviceType)}</h3><code>{selected.deviceType} · schema v{selected.version}</code></div>
          <div className="model-contract-policy"><span>自定义字段默认策略</span><small>提供端（Publisher）：{behaviorLabel(selected.custom.publisher.behavior)}</small><small>消费端（Consumer）：{behaviorLabel(selected.custom.consumer.behavior)}</small></div>
        </header>
        <div className="model-field-toolbar">
          <div role="tablist" aria-label="属性字段级别">
            {(['all', 'required', 'optional', 'custom'] as LevelFilter[]).map((item) => <button key={item} className={level === item ? 'is-active' : ''} onClick={() => setLevel(item)}>{item === 'all' ? `全部（${selected.parameters.length}）` : `${parameterLevelLabel(item)}（${countLevel(selected, item)}）`}</button>)}
          </div>
          <input aria-label="搜索模型属性" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索名称、路径、类型或单位" />
        </div>
        <div className="model-property-tree">
          {[...endpoints].map(([endpointId, capabilities]) => <section className="model-endpoint" key={endpointId}>
            <header><span>第一级 · 端点（Endpoint）</span><strong>{resourceLabel(endpointId)}</strong><code>{endpointId}</code></header>
            {[...capabilities].map(([capabilityId, parameters]) => <section className="model-capability" key={capabilityId}>
              <header><span>第二级 · 能力（Capability）</span><strong>{resourceLabel(capabilityId)}</strong><code>{endpointId} / {capabilityId}</code></header>
              <div>{parameters.map((parameter) => <article className="model-property-card" key={`${endpointId}/${capabilityId}/${parameter.path.propertyId}`}>
                <div className="model-property-title"><span className={`parameter-level is-${parameter.level}`}>{parameterLevelLabel(parameter.level)}</span><div><small>第三级 · 属性（Property）</small><strong>{propertyDisplayLabel(parameter.name, parameter.path.propertyId)}</strong><code>{endpointId} / {capabilityId} / {parameter.path.propertyId}</code></div><em>{parameter.level === 'custom' ? '数据库可配置' : '内置只读'}</em></div>
                <dl>
                  <div><dt>值类型（type）</dt><dd>{valueTypeLabel(parameter.type)}</dd></div>
                  <div><dt>单位（unit）</dt><dd>{parameter.unit ? unitLabel(parameter.unit) : '无（none）'}</dd></div>
                  <div><dt>权限（permissions）</dt><dd>{permissionLabel(parameter.readable, parameter.writable, parameter.notifiable)}</dd></div>
                  <div><dt>取值约束</dt><dd>{valueConstraint(parameter)}</dd></div>
                  <div><dt>提供端级别</dt><dd>{parameterLevelLabel(parameter.publisher.level)} · {behaviorLabel(parameter.publisher.behavior)}</dd></div>
                  <div><dt>消费端级别</dt><dd>{parameterLevelLabel(parameter.consumer.level)} · {behaviorLabel(parameter.consumer.behavior)}</dd></div>
                  <div><dt>提供端说明</dt><dd>{parameter.publisherNotes ?? '—'}</dd></div>
                  <div><dt>消费端说明</dt><dd>{parameter.consumerNotes ?? '—'}</dd></div>
                  <div><dt>状态过期（staleAfterSeconds）</dt><dd>{parameter.staleAfterSeconds ? `${parameter.staleAfterSeconds} 秒` : '跟随设备定义'}</dd></div>
                </dl>
              </article>)}</div>
            </section>)}
          </section>)}
          {visibleParameters.length === 0 && <div className="empty-state">没有符合当前筛选条件的属性字段。</div>}
        </div>
        <CustomModelPropertyManager deviceType={selected.deviceType} embedded onChanged={() => void refresh()} />
      </div>}
    </div>
  </section>
}
