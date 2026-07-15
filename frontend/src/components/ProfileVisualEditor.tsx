import { useMemo, useState } from 'react'
import { previewMapping } from '../api/mapping'
import { profileKindLabel, transformTypeLabel, valueTypeLabel } from '../presentationLabels'
import type { PropertyValue, ValueType } from '../types/device'
import type { MappingDirection, MappingPreviewRequest, MappingPreviewResult, MappingProfile, MappingTransform, MappingTransformType } from '../types/mapping'

type PreviewFunction = (input: MappingPreviewRequest) => Promise<MappingPreviewResult>

const valueTypes: ValueType[] = ['bool', 'int', 'number', 'string', 'enum']
const transformTypes: MappingTransformType[] = ['invert', 'scale', 'map-range', 'round', 'unit', 'range-enum', 'threshold', 'bool-enum', 'enum-bool', 'enum', 'parse-number', 'number-string', 'clamp']
const unitRoutes = [
  ['celsius', 'fahrenheit'], ['fahrenheit', 'celsius'], ['celsius', 'kelvin'],
  ['kelvin', 'celsius'], ['ratio', 'percent'], ['percent', 'ratio'],
  ['celsius', 'celsius'], ['fahrenheit', 'fahrenheit'], ['kelvin', 'kelvin'],
  ['ratio', 'ratio'], ['percent', 'percent'],
] as const

function outputType(input: ValueType, transforms: MappingTransform[]): ValueType {
  return transforms.reduce<ValueType>((current, transform) => {
    if (transform.type === 'scale' || transform.type === 'unit' || transform.type === 'clamp' || transform.type === 'map-range' || transform.type === 'parse-number') return 'number'
    if (transform.type === 'range-enum' || transform.type === 'bool-enum') return 'enum'
    if (transform.type === 'threshold' || transform.type === 'enum-bool') return 'bool'
    if (transform.type === 'round') return 'int'
    if (transform.type === 'number-string') return 'string'
    return current
  }, input)
}

function defaultTransform(type: MappingTransformType): MappingTransform {
  if (type === 'scale') return { type, factor: 1, offset: 0 }
  if (type === 'unit') return { type, fromUnit: 'celsius', toUnit: 'fahrenheit' }
  if (type === 'enum') return { type, values: { off: 'inactive', on: 'active' } }
  if (type === 'clamp') return { type, min: 0, max: 100 }
  if (type === 'range-enum') return { type, bands: [{ max: 18, value: 'cold', reverse: 10 }, { max: 28, value: 'comfortable', reverse: 24 }, { value: 'hot', reverse: 32 }] }
  if (type === 'threshold') return { type, threshold: 20, operator: 'gte', trueNumber: 25, falseNumber: 10 }
  if (type === 'bool-enum') return { type, trueValue: 'active', falseValue: 'inactive' }
  if (type === 'enum-bool') return { type, trueValue: 'active', falseValue: 'inactive' }
  if (type === 'map-range') return { type, inputMin: 0, inputMax: 100, outputMin: 0, outputMax: 1 }
  if (type === 'round') return { type, mode: 'nearest' }
  return { type }
}

function supported(type: MappingTransformType, input: ValueType): boolean {
  if (type === 'invert') return input === 'bool'
  if (type === 'enum') return input === 'enum' || input === 'string'
  if (type === 'bool-enum') return input === 'bool'
  if (type === 'enum-bool') return input === 'enum' || input === 'string'
  if (type === 'parse-number') return input === 'string'
  if (type === 'number-string') return input === 'int' || input === 'number'
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

function thresholdResult(value: number, threshold: number, operator: MappingTransform['operator']): boolean {
  if (operator === 'gt') return value > threshold
  if (operator === 'lte') return value <= threshold
  if (operator === 'lt') return value < threshold
  return value >= threshold
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
    if (transform.type === 'range-enum') {
      const bands = transform.bands ?? []
      if (bands.length < 2) errors.push(`第 ${index + 1} 步至少需要两个数值分段。`)
      if (bands.length > 0 && bands[bands.length - 1].max !== undefined) errors.push(`第 ${index + 1} 步最后一个分段的上限必须留空。`)
      if (bands.slice(0, -1).some((band) => !Number.isFinite(band.max))) errors.push(`第 ${index + 1} 步除最后一段外都必须填写有限上限。`)
      if (new Set(bands.map((band) => band.value)).size !== bands.length || bands.some((band) => !band.value.trim())) errors.push(`第 ${index + 1} 步分段枚举值必须非空且唯一。`)
      bands.forEach((band, bandIndex) => {
        const previous = bandIndex > 0 ? bands[bandIndex - 1].max : undefined
        if (band.max !== undefined && previous !== undefined && band.max <= previous) errors.push(`第 ${index + 1} 步分段上限必须严格递增。`)
        if (!Number.isFinite(band.reverse) || (previous !== undefined && band.reverse <= previous) || (band.max !== undefined && band.reverse > band.max)) errors.push(`第 ${index + 1} 步每个反向代表值必须位于对应分段内。`)
      })
    }
    if (transform.type === 'threshold') {
      if (!Number.isFinite(transform.threshold) || !Number.isFinite(transform.trueNumber) || !Number.isFinite(transform.falseNumber)) errors.push(`第 ${index + 1} 步阈值及反向代表值必须是有限数值。`)
      if (!transform.operator) errors.push(`第 ${index + 1} 步必须选择阈值比较方式。`)
      if (Number.isFinite(transform.threshold) && Number.isFinite(transform.trueNumber) && Number.isFinite(transform.falseNumber) && transform.operator) {
        if (!thresholdResult(transform.trueNumber!, transform.threshold!, transform.operator)) errors.push(`第 ${index + 1} 步 true 反向值必须满足阈值条件。`)
        if (thresholdResult(transform.falseNumber!, transform.threshold!, transform.operator)) errors.push(`第 ${index + 1} 步 false 反向值不能满足阈值条件。`)
      }
    }
    if ((transform.type === 'bool-enum' || transform.type === 'enum-bool') && (!transform.trueValue?.trim() || !transform.falseValue?.trim() || transform.trueValue === transform.falseValue)) errors.push(`第 ${index + 1} 步 true / false 对应值必须非空且不同。`)
    if (transform.type === 'map-range') {
      const values = [transform.inputMin, transform.inputMax, transform.outputMin, transform.outputMax]
      if (values.some((value) => !Number.isFinite(value))) errors.push(`第 ${index + 1} 步四个区间边界必须是有限数值。`)
      if (transform.inputMin === transform.inputMax || transform.outputMin === transform.outputMax) errors.push(`第 ${index + 1} 步输入和输出区间长度都不能为零。`)
    }
    if (transform.type === 'round' && !transform.mode) errors.push(`第 ${index + 1} 步必须选择取整方式。`)
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
  const bands = transform.bands ?? []
  return <article className="profile-transform-card">
    <header><span>{String(index + 1).padStart(2, '0')}</span><div><strong>{transformTypeLabel(transform.type)}</strong><small>{transform.type === 'clamp' ? '仅支持正向预览；当前设备属性映射要求可逆' : '支持正向与反向执行（forward / reverse）'}</small></div><div><button aria-label={`上移第 ${index + 1} 步`} disabled={index === 0} onClick={() => onMove(-1)}>↑</button><button aria-label={`下移第 ${index + 1} 步`} disabled={index === count - 1} onClick={() => onMove(1)}>↓</button><button aria-label={`删除第 ${index + 1} 步`} onClick={onRemove}>×</button></div></header>
    {transform.type === 'scale' && <div className="profile-transform-fields"><label>缩放系数（factor）<input aria-label={`第 ${index + 1} 步缩放系数`} type="number" step="any" value={transform.factor ?? 1} onChange={(event) => onChange({ ...transform, factor: Number(event.target.value) })} /></label><label>偏移量（offset）<input aria-label={`第 ${index + 1} 步偏移量`} type="number" step="any" value={transform.offset ?? 0} onChange={(event) => onChange({ ...transform, offset: Number(event.target.value) })} /></label></div>}
    {transform.type === 'clamp' && <div className="profile-transform-fields"><label>最小值（min）<input aria-label={`第 ${index + 1} 步最小值`} type="number" step="any" value={transform.min ?? ''} onChange={(event) => onChange({ ...transform, min: event.target.value === '' ? undefined : Number(event.target.value) })} /></label><label>最大值（max）<input aria-label={`第 ${index + 1} 步最大值`} type="number" step="any" value={transform.max ?? ''} onChange={(event) => onChange({ ...transform, max: event.target.value === '' ? undefined : Number(event.target.value) })} /></label></div>}
    {transform.type === 'unit' && <label className="profile-unit-route">单位路径（fromUnit → toUnit）<select aria-label={`第 ${index + 1} 步单位路径`} value={`${transform.fromUnit}:${transform.toUnit}`} onChange={(event) => { const [fromUnit, toUnit] = event.target.value.split(':'); onChange({ type: 'unit', fromUnit, toUnit }) }}>{unitRoutes.map(([from, to]) => <option key={`${from}:${to}`} value={`${from}:${to}`}>{from} → {to}</option>)}</select></label>}
    {transform.type === 'enum' && <div className="profile-enum-map"><div className="profile-enum-head"><span>来源值（source）</span><span>目标值（target）</span><span /></div>{values.map(([source, target], row) => <div key={`${source}-${row}`}><input aria-label={`第 ${index + 1} 步来源值 ${row + 1}`} value={source} onChange={(event) => { const next = Object.fromEntries(values.map(([key, value], item) => item === row ? [event.target.value, value] : [key, value])); onChange({ ...transform, values: next }) }} /><span>→</span><input aria-label={`第 ${index + 1} 步目标值 ${row + 1}`} value={target} onChange={(event) => { const next = Object.fromEntries(values.map(([key, value], item) => item === row ? [key, event.target.value] : [key, value])); onChange({ ...transform, values: next }) }} /><button aria-label={`删除第 ${index + 1} 步枚举行 ${row + 1}`} onClick={() => onChange({ ...transform, values: Object.fromEntries(values.filter((_, item) => item !== row)) })}>×</button></div>)}<button onClick={() => onChange({ ...transform, values: { ...(transform.values ?? {}), [`value-${values.length + 1}`]: `mapped-${values.length + 1}` } })}>＋ 添加枚举值</button></div>}
    {transform.type === 'range-enum' && <div className="profile-range-bands"><div className="profile-range-head"><span>数值上限（≤ max）</span><span>枚举输出（value）</span><span>反向代表值（reverse）</span><span /></div>{bands.map((band, row) => <div key={row}><input aria-label={`第 ${index + 1} 步分段上限 ${row + 1}`} type="number" step="any" placeholder={row === bands.length - 1 ? '+∞（留空）' : undefined} value={band.max ?? ''} onChange={(event) => onChange({ ...transform, bands: bands.map((item, itemIndex) => itemIndex === row ? { ...item, max: event.target.value === '' ? undefined : Number(event.target.value) } : item) })} /><input aria-label={`第 ${index + 1} 步分段枚举 ${row + 1}`} value={band.value} onChange={(event) => onChange({ ...transform, bands: bands.map((item, itemIndex) => itemIndex === row ? { ...item, value: event.target.value } : item) })} /><input aria-label={`第 ${index + 1} 步分段反向值 ${row + 1}`} type="number" step="any" value={band.reverse} onChange={(event) => onChange({ ...transform, bands: bands.map((item, itemIndex) => itemIndex === row ? { ...item, reverse: Number(event.target.value) } : item) })} /><button aria-label={`删除第 ${index + 1} 步分段 ${row + 1}`} onClick={() => onChange({ ...transform, bands: bands.filter((_, itemIndex) => itemIndex !== row) })}>×</button></div>)}<p>按顺序匹配“数值 ≤ 上限”；最后一行上限留空表示剩余范围。反向写入枚举时使用代表值。</p><button onClick={() => { const fallback = bands.at(-1) ?? { value: 'high', reverse: 100 }; const previousMax = bands.length > 1 ? bands[bands.length - 2].max ?? 0 : 0; const max = previousMax + 10; onChange({ ...transform, bands: [...bands.slice(0, -1), { max, value: `band-${bands.length}`, reverse: previousMax + 5 }, fallback] }) }}>＋ 在最终分段前添加</button></div>}
    {transform.type === 'threshold' && <div className="profile-transform-fields profile-threshold-fields"><label>比较方式（operator）<select aria-label={`第 ${index + 1} 步比较方式`} value={transform.operator ?? 'gte'} onChange={(event) => onChange({ ...transform, operator: event.target.value as MappingTransform['operator'] })}><option value="gte">大于等于（≥）</option><option value="gt">大于（&gt;）</option><option value="lte">小于等于（≤）</option><option value="lt">小于（&lt;）</option></select></label><label>阈值（threshold）<input aria-label={`第 ${index + 1} 步阈值`} type="number" step="any" value={transform.threshold ?? ''} onChange={(event) => onChange({ ...transform, threshold: Number(event.target.value) })} /></label><label>true 反向值<input aria-label={`第 ${index + 1} 步 true 反向值`} type="number" step="any" value={transform.trueNumber ?? ''} onChange={(event) => onChange({ ...transform, trueNumber: Number(event.target.value) })} /></label><label>false 反向值<input aria-label={`第 ${index + 1} 步 false 反向值`} type="number" step="any" value={transform.falseNumber ?? ''} onChange={(event) => onChange({ ...transform, falseNumber: Number(event.target.value) })} /></label></div>}
    {(transform.type === 'bool-enum' || transform.type === 'enum-bool') && <div className="profile-transform-fields"><label>true 对应值<input aria-label={`第 ${index + 1} 步 true 对应值`} value={transform.trueValue ?? ''} onChange={(event) => onChange({ ...transform, trueValue: event.target.value })} /></label><label>false 对应值<input aria-label={`第 ${index + 1} 步 false 对应值`} value={transform.falseValue ?? ''} onChange={(event) => onChange({ ...transform, falseValue: event.target.value })} /></label></div>}
    {transform.type === 'map-range' && <div className="profile-transform-fields profile-range-fields"><label>输入最小值（inputMin）<input aria-label={`第 ${index + 1} 步输入最小值`} type="number" step="any" value={transform.inputMin ?? ''} onChange={(event) => onChange({ ...transform, inputMin: Number(event.target.value) })} /></label><label>输入最大值（inputMax）<input aria-label={`第 ${index + 1} 步输入最大值`} type="number" step="any" value={transform.inputMax ?? ''} onChange={(event) => onChange({ ...transform, inputMax: Number(event.target.value) })} /></label><label>输出最小值（outputMin）<input aria-label={`第 ${index + 1} 步输出最小值`} type="number" step="any" value={transform.outputMin ?? ''} onChange={(event) => onChange({ ...transform, outputMin: Number(event.target.value) })} /></label><label>输出最大值（outputMax）<input aria-label={`第 ${index + 1} 步输出最大值`} type="number" step="any" value={transform.outputMax ?? ''} onChange={(event) => onChange({ ...transform, outputMax: Number(event.target.value) })} /></label></div>}
    {transform.type === 'round' && <label className="profile-unit-route">取整方式（mode）<select aria-label={`第 ${index + 1} 步取整方式`} value={transform.mode ?? 'nearest'} onChange={(event) => onChange({ ...transform, mode: event.target.value as MappingTransform['mode'] })}><option value="nearest">四舍五入（nearest）</option><option value="floor">向下取整（floor）</option><option value="ceil">向上取整（ceil）</option></select></label>}
    {transform.type === 'parse-number' && <p className="profile-transform-note">将数字文本解析为 number；反向写入时生成不带多余零的规范文本。</p>}
    {transform.type === 'number-string' && <p className="profile-transform-note">将 int / number 格式化为文本；反向写入时解析并校验数值类型。</p>}
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
