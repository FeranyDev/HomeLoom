import { useMemo, useState } from 'react'
import { previewMapping } from '../api/mapping'
import { profileKindLabel, transformTypeLabel, valueTypeLabel } from '../presentationLabels'
import type { PropertyValue, ValueType } from '../types/device'
import type { MappingDirection, MappingPreviewRequest, MappingPreviewResult, MappingProfile, MappingTransform, MappingTransformType } from '../types/mapping'

type PreviewFunction = (input: MappingPreviewRequest) => Promise<MappingPreviewResult>

const valueTypes: ValueType[] = ['bool', 'int', 'number', 'string', 'enum']
const transformTypes: MappingTransformType[] = ['invert', 'scale', 'unit', 'enum', 'clamp']
const unitRoutes = [
  ['celsius', 'fahrenheit'], ['fahrenheit', 'celsius'], ['celsius', 'kelvin'],
  ['kelvin', 'celsius'], ['ratio', 'percent'], ['percent', 'ratio'],
  ['celsius', 'celsius'], ['fahrenheit', 'fahrenheit'], ['kelvin', 'kelvin'],
  ['ratio', 'ratio'], ['percent', 'percent'],
] as const

function outputType(input: ValueType, transforms: MappingTransform[]): ValueType {
  return transforms.reduce<ValueType>((current, transform) =>
    transform.type === 'scale' || transform.type === 'unit' || transform.type === 'clamp' ? 'number' : current, input)
}

function defaultTransform(type: MappingTransformType): MappingTransform {
  if (type === 'scale') return { type, factor: 1, offset: 0 }
  if (type === 'unit') return { type, fromUnit: 'celsius', toUnit: 'fahrenheit' }
  if (type === 'enum') return { type, values: { off: 'inactive', on: 'active' } }
  if (type === 'clamp') return { type, min: 0, max: 100 }
  return { type }
}

function supported(type: MappingTransformType, input: ValueType): boolean {
  if (type === 'invert') return input === 'bool'
  if (type === 'enum') return input === 'enum' || input === 'string'
  return input === 'int' || input === 'number'
}

function propertyValue(type: ValueType, raw: string): PropertyValue {
  if (type === 'bool') return { type, bool: raw === 'true' }
  if (type === 'int') return { type, int: Number(raw) }
  if (type === 'number') return { type, number: Number(raw) }
  return { type, string: raw }
}

function valueText(value: PropertyValue): string {
  if (value.bool !== undefined) return String(value.bool)
  if (value.int !== undefined) return String(value.int)
  if (value.number !== undefined) return Number(value.number.toFixed(6)).toString()
  return value.string ?? '—'
}

function localErrors(profile: MappingProfile): string[] {
  const errors: string[] = []
  if (!/^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/.test(profile.id)) errors.push('标识（ID）需为 1–64 位小写字母、数字、点、下划线或连字符，且首尾为字母或数字。')
  if (profile.version < 1) errors.push('版本（version）必须大于 0。')
  if (profile.default && profile.default.type !== profile.inputType) errors.push('默认值（default）的类型必须与输入类型一致。')
  let current = profile.inputType
  profile.transforms.forEach((transform, index) => {
    if (!supported(transform.type, current)) errors.push(`第 ${index + 1} 步“${transformTypeLabel(transform.type)}”不支持 ${valueTypeLabel(current)}。`)
    if (transform.type === 'scale' && (!Number.isFinite(transform.factor ?? 1) || (transform.factor ?? 1) === 0)) errors.push(`第 ${index + 1} 步缩放系数必须是非零有限数值。`)
    if (transform.type === 'clamp' && transform.min === undefined && transform.max === undefined) errors.push(`第 ${index + 1} 步至少需要一个范围边界。`)
    if (transform.type === 'clamp' && transform.min !== undefined && transform.max !== undefined && transform.min > transform.max) errors.push(`第 ${index + 1} 步最小值不能大于最大值。`)
    if (transform.type === 'enum') {
      const values = Object.entries(transform.values ?? {})
      if (values.length === 0) errors.push(`第 ${index + 1} 步至少需要一组枚举映射。`)
      if (values.some(([source, target]) => !source || !target)) errors.push(`第 ${index + 1} 步枚举来源值和目标值不能为空。`)
      if (new Set(values.map(([, target]) => target)).size !== values.length) errors.push(`第 ${index + 1} 步枚举目标值必须唯一，才能反向转换。`)
    }
    if (transform.type === 'unit' && !unitRoutes.some(([from, to]) => from === transform.fromUnit && to === transform.toUnit) && transform.fromUnit !== transform.toUnit) errors.push(`第 ${index + 1} 步单位路径不受支持。`)
    current = outputType(current, [transform])
  })
  if (current !== profile.outputType) errors.push(`转换链实际输出 ${valueTypeLabel(current)}，与配置的 ${valueTypeLabel(profile.outputType)} 不一致。`)
  return errors
}

function rawDefault(type: ValueType): string {
  if (type === 'bool') return 'true'
  if (type === 'enum' || type === 'string') return 'off'
  return '20'
}

function ValueInput({ label, type, value, onChange }: { label: string; type: ValueType; value: string; onChange: (value: string) => void }) {
  if (type === 'bool') return <label>{label}<select aria-label={label} value={value} onChange={(event) => onChange(event.target.value)}><option value="true">是（true）</option><option value="false">否（false）</option></select></label>
  return <label>{label}<input aria-label={label} type={type === 'number' || type === 'int' ? 'number' : 'text'} step={type === 'number' ? 'any' : undefined} value={value} onChange={(event) => onChange(event.target.value)} /></label>
}

function TransformEditor({ transform, index, count, onChange, onMove, onRemove }: {
  transform: MappingTransform; index: number; count: number
  onChange: (value: MappingTransform) => void; onMove: (offset: -1 | 1) => void; onRemove: () => void
}) {
  const values = Object.entries(transform.values ?? {})
  return <article className="profile-transform-card">
    <header><span>{String(index + 1).padStart(2, '0')}</span><div><strong>{transformTypeLabel(transform.type)}</strong><small>{transform.type === 'clamp' ? '仅支持正向预览；当前设备属性映射要求可逆' : '支持正向与反向执行（forward / reverse）'}</small></div><div><button aria-label={`上移第 ${index + 1} 步`} disabled={index === 0} onClick={() => onMove(-1)}>↑</button><button aria-label={`下移第 ${index + 1} 步`} disabled={index === count - 1} onClick={() => onMove(1)}>↓</button><button aria-label={`删除第 ${index + 1} 步`} onClick={onRemove}>×</button></div></header>
    {transform.type === 'scale' && <div className="profile-transform-fields"><label>缩放系数（factor）<input aria-label={`第 ${index + 1} 步缩放系数`} type="number" step="any" value={transform.factor ?? 1} onChange={(event) => onChange({ ...transform, factor: Number(event.target.value) })} /></label><label>偏移量（offset）<input aria-label={`第 ${index + 1} 步偏移量`} type="number" step="any" value={transform.offset ?? 0} onChange={(event) => onChange({ ...transform, offset: Number(event.target.value) })} /></label></div>}
    {transform.type === 'clamp' && <div className="profile-transform-fields"><label>最小值（min）<input aria-label={`第 ${index + 1} 步最小值`} type="number" step="any" value={transform.min ?? ''} onChange={(event) => onChange({ ...transform, min: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><label>最大值（max）<input aria-label={`第 ${index + 1} 步最大值`} type="number" step="any" value={transform.max ?? ''} onChange={(event) => onChange({ ...transform, max: event.target.value === '' ? undefined : Number(event.target.value) })} /></label></div>}
    {transform.type === 'unit' && <label className="profile-unit-route">单位路径（fromUnit → toUnit）<select aria-label={`第 ${index + 1} 步单位路径`} value={`${transform.fromUnit}:${transform.toUnit}`} onChange={(event) => { const [fromUnit, toUnit] = event.target.value.split(':'); onChange({ type: 'unit', fromUnit, toUnit }) }}>{unitRoutes.map(([from, to]) => <option key={`${from}:${to}`} value={`${from}:${to}`}>{from} → {to}</option>)}</select></label>}
    {transform.type === 'enum' && <div className="profile-enum-map"><div className="profile-enum-head"><span>来源值（source）</span><span>目标值（target）</span><span /></div>{values.map(([source, target], row) => <div key={`${source}-${row}`}><input aria-label={`第 ${index + 1} 步来源值 ${row + 1}`} value={source} onChange={(event) => { const next = Object.fromEntries(values.map(([key, value], item) => item === row ? [event.target.value, value] : [key, value])); onChange({ ...transform, values: next }) }} /><span>→</span><input aria-label={`第 ${index + 1} 步目标值 ${row + 1}`} value={target} onChange={(event) => { const next = Object.fromEntries(values.map(([key, value], item) => item === row ? [key, event.target.value] : [key, value])); onChange({ ...transform, values: next }) }} /><button aria-label={`删除第 ${index + 1} 步枚举行 ${row + 1}`} onClick={() => onChange({ ...transform, values: Object.fromEntries(values.filter((_, item) => item !== row)) })}>×</button></div>)}<button onClick={() => onChange({ ...transform, values: { ...(transform.values ?? {}), [`value-${values.length + 1}`]: `mapped-${values.length + 1}` } })}>＋ 添加枚举值</button></div>}
    {transform.type === 'invert' && <p className="profile-transform-note">布尔值自动执行 true ↔ false，无需附加参数。</p>}
  </article>
}

export function ProfileVisualEditor({ initialProfile, editing, saving, onClose, onSave, runPreview = previewMapping }: {
  initialProfile: MappingProfile; editing: boolean; saving: boolean
  onClose: () => void; onSave: (profile: MappingProfile) => Promise<void>; runPreview?: PreviewFunction
}) {
  const [profile, setProfile] = useState(initialProfile)
  const [editorMode, setEditorMode] = useState<'visual' | 'json'>('visual')
  const [document, setDocument] = useState(JSON.stringify(initialProfile, null, 2))
  const [documentError, setDocumentError] = useState<string | null>(null)
  const [direction, setDirection] = useState<MappingDirection>('forward')
  const [previewInput, setPreviewInput] = useState(rawDefault(initialProfile.inputType))
  const [previewResult, setPreviewResult] = useState<MappingPreviewResult | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const errors = useMemo(() => localErrors(profile), [profile])
  const reverseAvailable = !profile.transforms.some((transform) => transform.type === 'clamp')
  const activeInputType = direction === 'forward' ? profile.inputType : profile.outputType

  const replaceTransforms = (transforms: MappingTransform[]) => setProfile((current) => ({ ...current, transforms, outputType: outputType(current.inputType, transforms) }))
  const changeInputType = (inputType: ValueType) => {
    setProfile((current) => ({ ...current, inputType, outputType: inputType, transforms: [], default: undefined }))
    setPreviewInput(rawDefault(inputType)); setPreviewResult(null); setPreviewError(null)
  }
  const switchMode = (next: 'visual' | 'json') => {
    if (next === 'json') { setDocument(JSON.stringify(profile, null, 2)); setDocumentError(null); setEditorMode(next); return }
    try {
      const parsed = JSON.parse(document) as MappingProfile
      setProfile(parsed); setDocumentError(null); setEditorMode(next)
    } catch (cause) { setDocumentError(cause instanceof Error ? cause.message : 'JSON 格式无效') }
  }
  const save = async () => {
    if (editorMode === 'json') {
      try { await onSave(JSON.parse(document) as MappingProfile) } catch (cause) { setDocumentError(cause instanceof Error ? cause.message : 'JSON 格式无效') }
      return
    }
    if (errors.length === 0) await onSave(profile)
  }
  const preview = async () => {
    setPreviewing(true); setPreviewError(null); setPreviewResult(null)
    try { setPreviewResult(await runPreview({ profile, direction, value: propertyValue(activeInputType, previewInput) })) }
    catch (cause) { setPreviewError(cause instanceof Error ? cause.message : '预览失败') }
    finally { setPreviewing(false) }
  }

  return <section className="profile-editor profile-visual-editor" aria-label={editing ? `编辑 ${initialProfile.id}` : '新建 Profile'}>
    <header className="profile-editor-heading"><div><p className="eyebrow">可视化转换编排（VISUAL PIPELINE）</p><h3>{editing ? `编辑 ${initialProfile.id}` : '创建转换配置'}</h3></div><button aria-label="关闭 Profile 编辑器" onClick={onClose}>×</button></header>
    <ol className="profile-editor-progress" aria-label="配置流程"><li className="is-complete"><span>1</span><div><strong>定义接口</strong><small>用途与数据类型</small></div></li><li className={profile.transforms.length ? 'is-complete' : 'is-current'}><span>2</span><div><strong>编排转换</strong><small>{profile.transforms.length} 个处理步骤</small></div></li><li className={previewResult ? 'is-complete' : 'is-current'}><span>3</span><div><strong>验证结果</strong><small>正向 / 反向预览</small></div></li></ol>
    <div className="profile-editor-modes" role="tablist" aria-label="Profile 编辑方式"><button className={editorMode === 'visual' ? 'is-active' : ''} role="tab" aria-selected={editorMode === 'visual'} onClick={() => switchMode('visual')}>可视化配置</button><button className={editorMode === 'json' ? 'is-active' : ''} role="tab" aria-selected={editorMode === 'json'} onClick={() => switchMode('json')}>高级 JSON</button></div>
    {editorMode === 'json' ? <div className="profile-json-editor"><textarea aria-label="Profile JSON" rows={22} value={document} onChange={(event) => setDocument(event.target.value)} spellCheck={false} />{documentError && <p className="field-error" role="alert">{documentError}</p>}<p>适合复制、审阅或完整调整原始 Profile；切回可视化配置时会解析当前内容。</p></div> : <>
      <section className="profile-definition-card"><header><span>01</span><div><strong>定义转换接口</strong><small>Profile 会在设备映射中按用途和数据类型自动筛选。</small></div></header><div className="profile-definition-grid"><label>标识（ID）<input aria-label="Profile 标识" disabled={editing} value={profile.id} onChange={(event) => setProfile({ ...profile, id: event.target.value })} /></label><label>用途（kind）<select aria-label="Profile 用途" value={profile.kind} onChange={(event) => setProfile({ ...profile, kind: event.target.value as MappingProfile['kind'] })}><option value="provider">{profileKindLabel('provider')}</option><option value="capability">{profileKindLabel('capability')}</option><option value="target">{profileKindLabel('target')}</option></select></label><label>版本（version）<input aria-label="Profile 版本" type="number" min="1" value={profile.version} onChange={(event) => setProfile({ ...profile, version: Number(event.target.value) })} /></label><label>输入类型（inputType）<select aria-label="Profile 输入类型" value={profile.inputType} onChange={(event) => changeInputType(event.target.value as ValueType)}>{valueTypes.map((type) => <option key={type} value={type}>{valueTypeLabel(type)}</option>)}</select></label><div className="profile-derived-output"><span>推导输出类型（outputType）</span><strong>{valueTypeLabel(profile.outputType)}</strong><small>随转换步骤自动更新</small></div><div className="profile-default-field"><label><input aria-label="启用缺失值默认回退" type="checkbox" checked={profile.default !== undefined} onChange={(event) => setProfile({ ...profile, default: event.target.checked ? propertyValue(profile.inputType, rawDefault(profile.inputType)) : undefined })} />缺失值默认回退（default）</label>{profile.default && <ValueInput label={`默认输入值 · ${valueTypeLabel(profile.inputType)}`} type={profile.inputType} value={valueText(profile.default)} onChange={(value) => setProfile({ ...profile, default: propertyValue(profile.inputType, value) })} />}<small>仅在来源值缺失时作为流水线输入；默认关闭。</small></div></div></section>
      <section className="profile-pipeline-builder"><header><span>02</span><div><strong>编排转换步骤</strong><small>步骤按从左到右、从上到下顺序执行；反向写入会逆序执行。</small></div></header><div className="profile-pipeline-summary"><div><span>输入（input）</span><strong>{valueTypeLabel(profile.inputType)}</strong></div><i>→</i>{profile.transforms.length ? profile.transforms.map((transform, index) => <div key={`${transform.type}-${index}`}><span>步骤 {index + 1}</span><strong>{transformTypeLabel(transform.type)}</strong></div>) : <div className="is-identity"><span>无步骤</span><strong>恒等转换（identity）</strong></div>}<i>→</i><div><span>输出（output）</span><strong>{valueTypeLabel(profile.outputType)}</strong></div></div><div className="profile-transform-list">{profile.transforms.map((transform, index) => <TransformEditor key={`${transform.type}-${index}`} transform={transform} index={index} count={profile.transforms.length} onChange={(value) => replaceTransforms(profile.transforms.map((item, itemIndex) => itemIndex === index ? value : item))} onMove={(offset) => { const next = [...profile.transforms]; const [item] = next.splice(index, 1); next.splice(index + offset, 0, item); replaceTransforms(next) }} onRemove={() => replaceTransforms(profile.transforms.filter((_, itemIndex) => itemIndex !== index))} />)}</div><div className="profile-transform-picker"><span>添加转换步骤</span>{transformTypes.map((type) => { const available = supported(type, profile.outputType); return <button key={type} disabled={!available} title={available ? '' : `不支持 ${valueTypeLabel(profile.outputType)}`} onClick={() => replaceTransforms([...profile.transforms, defaultTransform(type)])}>＋ {transformTypeLabel(type)}</button> })}</div></section>
      <section className="profile-preview-card"><header><span>03</span><div><strong>使用样本验证</strong><small>直接运行当前草稿，不写入数据库或真实设备。</small></div></header><div className="profile-preview-layout"><form onSubmit={(event) => { event.preventDefault(); void preview() }}><div className="profile-direction-toggle" role="group" aria-label="预览方向"><button type="button" className={direction === 'forward' ? 'is-active' : ''} onClick={() => { setDirection('forward'); setPreviewInput(rawDefault(profile.inputType)); setPreviewResult(null) }}>正向（forward）</button><button type="button" className={direction === 'reverse' ? 'is-active' : ''} disabled={!reverseAvailable} title={reverseAvailable ? '' : '范围裁剪无法反向执行'} onClick={() => { setDirection('reverse'); setPreviewInput(rawDefault(profile.outputType)); setPreviewResult(null) }}>反向（reverse）</button></div><ValueInput label={`样本输入 · ${valueTypeLabel(activeInputType)}`} type={activeInputType} value={previewInput} onChange={setPreviewInput} /><button className="add-button" disabled={previewing || errors.length > 0}>{previewing ? '计算中…' : '运行当前草稿'}</button>{!reverseAvailable && <small>当前包含范围裁剪（clamp），仅可验证正向流程。</small>}{previewError && <p className="field-error" role="alert">{previewError}</p>}</form><div className="profile-preview-result" aria-live="polite">{previewResult ? <><span>最终输出 · {valueTypeLabel(previewResult.value.type)}</span><strong>{valueText(previewResult.value)}</strong><ol>{previewResult.steps.map((step) => <li key={`${step.index}-${step.transform}`}><span>{step.index + 1}. {transformTypeLabel(step.transform as MappingTransformType)}</span><code>{step.input ? valueText(step.input) : 'null'} → {valueText(step.output)}</code></li>)}</ol></> : <div className="empty-state">输入一个真实样本，运行后会在这里展示最终值和每一步结果。</div>}</div></div></section>
      {errors.length > 0 ? <div className="profile-validation is-error"><strong>保存前还需处理 {errors.length} 项</strong><ul>{errors.map((item) => <li key={item}>{item}</li>)}</ul></div> : <div className={`profile-validation ${reverseAvailable ? 'is-ready' : 'is-limited'}`}><strong>配置结构有效</strong><span>{reverseAvailable ? '可用于当前双向设备属性映射' : '可以保存和正向预览，但不会出现在当前设备属性映射的可选列表中'}</span></div>}
    </>}
    <footer className="profile-editor-actions"><button onClick={onClose}>取消</button><button className="add-button" disabled={saving || (editorMode === 'visual' && errors.length > 0)} onClick={() => void save()}>{saving ? '保存中…' : '保存并热更新'}</button></footer>
  </section>
}
