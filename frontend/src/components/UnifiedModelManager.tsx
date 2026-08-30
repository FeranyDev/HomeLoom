import { useCallback, useEffect, useMemo, useState } from 'react'
import { createCustomModel, deleteCustomModel, deleteModelEnumOverride, listModelContracts, listModelEnumOverrides, upsertModelEnumOverride } from '../api/mapping'
import { bilingual, deviceTypeLabel, parameterLevelLabel, permissionLabel, propertyDisplayLabel, resourceLabel, unitLabel, valueTypeLabel } from '../presentationLabels'
import type { DeviceType, ParameterLevel } from '../types/device'
import type { ModelContract, ModelEnumOverride, ModelParameter } from '../types/mapping'
import { CustomModelPropertyManager } from './CustomModelPropertyManager'
import { HelpTooltip } from './HelpTooltip'

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

function modelLabel(model: ModelContract): string {
  return model.name ? bilingual(model.deviceType, model.name) : deviceTypeLabel(model.deviceType)
}

function valueConstraint(parameter: ModelParameter): string {
  if (parameter.enum?.length) return `枚举（enum）：${parameter.enum.join(' / ')}`
  const parts = [parameter.min !== undefined && `最小值（min）${parameter.min}`, parameter.max !== undefined && `最大值（max）${parameter.max}`, parameter.step !== undefined && `步长（step）${parameter.step}`].filter(Boolean)
  return parts.length ? parts.join(' · ') : '无额外约束'
}

export function UnifiedModelManager({ onModelCountChange }: { onModelCountChange?: (count: number) => void } = {}) {
  const [models, setModels] = useState<ModelContract[]>([])
  const [selectedType, setSelectedType] = useState<DeviceType | null>(null)
  const [level, setLevel] = useState<LevelFilter>('all')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [customCreateRevision, setCustomCreateRevision] = useState(0)
  const [creatingModel, setCreatingModel] = useState(false)
  const [modelDraft, setModelDraft] = useState({ deviceType: '', name: '', version: 1 })
  const [enumOverrides, setEnumOverrides] = useState<ModelEnumOverride[]>([])
  const [editingEnum, setEditingEnum] = useState<{ parameter: ModelParameter; text: string; original?: ModelEnumOverride } | null>(null)
  const [enumSaving, setEnumSaving] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const [result, overrides] = await Promise.all([listModelContracts(), listModelEnumOverrides().catch(() => [])])
      setModels(result)
      setEnumOverrides(overrides)
      onModelCountChange?.(result.length)
      window.dispatchEvent(new CustomEvent<number>('homeloom:model-count', { detail: result.length }))
      setSelectedType((current) => current && result.some((model) => model.deviceType === current) ? current : result[0]?.deviceType ?? null)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '读取统一模型失败')
    } finally {
      setLoading(false)
    }
  }, [onModelCountChange])

  useEffect(() => { void refresh() }, [refresh])
  const saveModel = async () => {
    try {
      const created = await createCustomModel(modelDraft)
      setCreatingModel(false)
      setModelDraft({ deviceType: '', name: '', version: 1 })
      await refresh()
      setSelectedType(created.deviceType)
    } catch (cause) { setError(cause instanceof Error ? cause.message : '新增统一模型失败') }
  }
  const removeModel = async (model: ModelContract) => {
    if (!window.confirm(`删除统一模型 ${modelLabel(model)}？`)) return
    try { await deleteCustomModel(model.deviceType); await refresh() } catch (cause) { setError(cause instanceof Error ? cause.message : '删除统一模型失败') }
  }

  const overrideFor = (parameter: ModelParameter) => enumOverrides.find((item) => item.deviceType === selectedType && item.endpointId === parameter.path.endpointId && item.capabilityId === parameter.path.capabilityId && item.propertyId === parameter.path.propertyId)
  const openEnumEditor = (parameter: ModelParameter) => {
    const original = overrideFor(parameter)
    setEditingEnum({ parameter, text: (parameter.enum ?? []).join(', '), original })
    setError('')
  }
  const saveEnumEditor = async () => {
    if (!editingEnum || !selectedType) return
    const values = editingEnum.text.split(',').map((item) => item.trim()).filter(Boolean)
    if (values.length === 0) {
      setError('枚举值不能为空')
      return
    }
    setEnumSaving(true)
    try {
      await upsertModelEnumOverride({
        id: editingEnum.original?.id,
        deviceType: selectedType,
        endpointId: editingEnum.parameter.path.endpointId,
        capabilityId: editingEnum.parameter.path.capabilityId,
        propertyId: editingEnum.parameter.path.propertyId,
        enum: values,
      })
      setEditingEnum(null)
      await refresh()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '保存枚举覆盖失败')
    } finally {
      setEnumSaving(false)
    }
  }
  const resetEnumEditor = async () => {
    if (!editingEnum?.original?.id) {
      setEditingEnum(null)
      return
    }
    if (!window.confirm('恢复该属性的内置/原始枚举定义？')) return
    setEnumSaving(true)
    try {
      await deleteModelEnumOverride(editingEnum.original.id)
      setEditingEnum(null)
      await refresh()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '恢复枚举定义失败')
    } finally {
      setEnumSaving(false)
    }
  }
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
    const requiredCapabilities = new Set(
      selected?.parameters
        .filter((parameter) => parameter.level === 'required')
        .map((parameter) => `${parameter.path.endpointId}/${parameter.path.capabilityId}`),
    )
    for (const parameter of visibleParameters) {
      const capabilities = result.get(parameter.path.endpointId) ?? new Map<string, ModelParameter[]>()
      const parameters = capabilities.get(parameter.path.capabilityId) ?? []
      parameters.push(parameter)
      capabilities.set(parameter.path.capabilityId, parameters)
      result.set(parameter.path.endpointId, capabilities)
    }
    for (const [endpointId, capabilities] of result) {
      const sortedCapabilities = [...capabilities].sort(([left], [right]) =>
        Number(requiredCapabilities.has(`${endpointId}/${right}`)) - Number(requiredCapabilities.has(`${endpointId}/${left}`)),
      )
      result.set(endpointId, new Map(sortedCapabilities.map(([capabilityId, parameters]) => [
        capabilityId,
        [...parameters].sort((left, right) => Number(right.level === 'required') - Number(left.level === 'required')),
      ])))
    }
    return result
  }, [selected, visibleParameters])

  if (loading) return <section className="model-catalog-loading" aria-label="统一模型加载中">正在读取统一模型…</section>
  if (error && models.length === 0) return <section className="model-catalog-loading"><p className="inline-error" role="alert">{error}</p><button onClick={() => void refresh()}>重新读取</button></section>

  return <section className="unified-model-manager">
    <header className="model-catalog-heading">
      <div><p className="eyebrow">统一智能设备模型（UNIFIED DEVICE MODELS）</p><h3><HelpTooltip content="统一模型连接设备来源和目标适配器；内置字段只读，自定义模型与字段保存后立即可用于映射。" label="统一模型说明">模型与属性字段配置</HelpTooltip></h3></div>
      <div className="model-catalog-summary"><div className="model-catalog-totals"><strong>{models.length}</strong><span>设备模型</span><strong>{models.reduce((total, model) => total + model.parameters.length, 0)}</strong><span>属性字段</span></div><button className="add-button" onClick={() => { setCreatingModel(true); setError('') }}>＋ 新增统一模型</button></div>
    </header>
    {error && <p className="inline-error" role="alert">{error}</p>}
    {creatingModel && <div className="custom-model-editor" role="dialog" aria-label="新增统一模型"><div className="form-heading"><div><p className="eyebrow">统一模型（UNIFIED MODEL）</p><h3><HelpTooltip content="先创建稳定标识，再添加端点、能力和属性。创建后立即可用于映射。" label="新建统一模型说明">新增统一模型</HelpTooltip></h3></div><button onClick={() => setCreatingModel(false)}>关闭</button></div><div className="custom-model-fields"><label>模型标识（deviceType）<input value={modelDraft.deviceType} onChange={(event) => setModelDraft({ ...modelDraft, deviceType: event.target.value })} placeholder="air-quality-monitor" /></label><label>显示名称（name）<input value={modelDraft.name} onChange={(event) => setModelDraft({ ...modelDraft, name: event.target.value })} placeholder="空气质量监测器" /></label><label>模型版本（version）<input type="number" min="1" value={modelDraft.version} onChange={(event) => setModelDraft({ ...modelDraft, version: Number(event.target.value) })} /></label></div><button className="add-button" onClick={() => void saveModel()}>创建并配置属性</button></div>}
    <div className="model-catalog-layout">
      <aside className="model-type-list" aria-label="统一设备模型列表" tabIndex={0}>
        {models.map((model) => <button key={model.deviceType} className={selectedType === model.deviceType ? 'is-selected' : ''} onClick={() => { setSelectedType(model.deviceType); setLevel('all'); setQuery('') }}>
          <span>{modelLabel(model)}</span><code>v{model.version}</code>
          <small><i className="is-required">{countLevel(model, 'required')}</i> 必需 · <i className="is-optional">{countLevel(model, 'optional')}</i> 可选 · <i className="is-custom">{countLevel(model, 'custom')}</i> 自定义</small>
        </button>)}
      </aside>
      {selected && <div className="model-contract-panel">
        <header className="model-contract-heading">
          <div><span>当前模型（deviceType）</span><h3>{modelLabel(selected)}</h3><code>{selected.deviceType} · schema v{selected.version}</code>{!selected.builtIn && <button className="danger-link model-delete-action" onClick={() => void removeModel(selected)}>删除自定义模型</button>}</div>
          <div className="model-contract-policy"><span>自定义字段默认策略</span><small>提供端（Publisher）：{behaviorLabel(selected.custom.publisher.behavior)}</small><small>消费端（Consumer）：{behaviorLabel(selected.custom.consumer.behavior)}</small></div>
          <div className="model-contract-actions"><span className="help-action"><button className="add-button" onClick={() => setCustomCreateRevision((current) => current + 1)}>＋ 新增自定义属性</button><HelpTooltip content="为当前模型新增端点、能力和属性三级字段。" label="新增属性说明" /></span></div>
        </header>
        <CustomModelPropertyManager deviceType={selected.deviceType} embedded createRevision={customCreateRevision} onChanged={() => void refresh()} />
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
                <div className="model-property-title"><span className={`parameter-level is-${parameter.level}`}>{parameterLevelLabel(parameter.level)}</span><div><small>第三级 · 属性（Property）</small><strong>{propertyDisplayLabel(parameter.name, parameter.path.propertyId)}</strong><code>{endpointId} / {capabilityId} / {parameter.path.propertyId}</code></div><div className="model-property-actions"><em>{parameter.type === 'enum' ? (overrideFor(parameter) ? '枚举已覆盖' : (parameter.level === 'custom' ? '自定义可改枚举' : '内置可覆盖枚举')) : (parameter.level === 'custom' ? '数据库可配置' : '内置只读')}</em>{parameter.type === 'enum' && <button onClick={() => openEnumEditor(parameter)}>编辑枚举</button>}</div></div>
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


      </div>}
    </div>
  {editingEnum && <div className="modal-backdrop is-enum-editor" onClick={() => !enumSaving && setEditingEnum(null)}>
    <div className="model-enum-dialog" role="dialog" aria-modal="true" aria-label="编辑统一模型枚举" onClick={(event) => event.stopPropagation()}>
      <div className="form-heading">
        <div>
          <p className="eyebrow">ENUM OVERRIDE</p>
          <h3>编辑枚举值 · {propertyDisplayLabel(editingEnum.parameter.name, editingEnum.parameter.path.propertyId)}</h3>
          <code>{editingEnum.parameter.path.endpointId} / {editingEnum.parameter.path.capabilityId} / {editingEnum.parameter.path.propertyId}</code>
        </div>
        <button onClick={() => !enumSaving && setEditingEnum(null)}>关闭</button>
      </div>
      <label><HelpTooltip content="修改立即生效，影响校验和属性映射；可恢复原始定义。" label="枚举覆盖说明">枚举值（逗号分隔）</HelpTooltip>
        <input
          aria-label="统一模型枚举值"
          autoFocus
          value={editingEnum.text}
          onChange={(event) => setEditingEnum({ ...editingEnum, text: event.target.value })}
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              event.preventDefault()
              if (!enumSaving) setEditingEnum(null)
            }
            if (event.key === 'Enter') {
              event.preventDefault()
              void saveEnumEditor()
            }
          }}
          placeholder="auto, manual, sleep"
        />
      </label>

      <div className="model-enum-actions">
        <button className="add-button" disabled={enumSaving} onClick={() => void saveEnumEditor()}>{enumSaving ? '保存中…' : '保存枚举覆盖'}</button>
        {editingEnum.original?.id && <button className="danger-link" disabled={enumSaving} onClick={() => void resetEnumEditor()}>恢复原始枚举</button>}
      </div>
    </div>
  </div>}
</section>
}
