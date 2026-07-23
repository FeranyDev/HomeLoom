import { useEffect, useState } from 'react'
import { listMappingProfiles, previewMapping } from '../api/mapping'
import type { PropertyValue, ValueType } from '../types/device'
import type { MappingDirection, MappingPreviewRequest, MappingPreviewResult, MappingProfileInfo, MappingTransform, MappingTransformType } from '../types/mapping'
import { transformTypeLabel, valueTypeLabel } from '../presentationLabels'

type PreviewFunction = (input: MappingPreviewRequest) => Promise<MappingPreviewResult>
type ProfileLoader = () => Promise<MappingProfileInfo[]>

function valueText(value: PropertyValue): string {
  if (value.bool !== undefined) return String(value.bool)
  if (value.int !== undefined) return String(value.int)
  if (value.number !== undefined) return Number(value.number.toFixed(6)).toString()
  return value.string ?? '—'
}

function defaultsFor(type: MappingTransformType): string {
  if (type === 'invert') return 'true'
  if (type === 'enum') return 'off'
  return '20'
}

function transformInputType(type: MappingTransformType): ValueType {
  if (type === 'invert') return 'bool'
  if (type === 'enum') return 'enum'
  if (type === 'int-number') return 'int'
  return 'number'
}

function enumOptions(profile: MappingProfileInfo, direction: MappingDirection): string[] {
  const values = profile.transforms.flatMap((transform) => {
    if (transform.type === 'enum') {
      const entries = Object.entries(transform.values ?? {})
      return direction === 'forward' ? entries.map(([source]) => source) : entries.map(([, target]) => String(target))
    }
    if (transform.type === 'range-enum' || transform.type === 'enum-number') return transform.bands?.map((band) => band.value) ?? []
    if (transform.type === 'bool-enum' || transform.type === 'enum-bool') return [transform.trueValue, transform.falseValue].filter((item): item is string => Boolean(item))
    return []
  })
  return [...new Set(values)]
}

function defaultInput(type: ValueType, options: string[] = []): string {
  if (type === 'bool') return 'true'
  if (type === 'string') return '示例文本'
  if (type === 'enum') return options[0] ?? 'off'
  return '20'
}

function inputValue(type: ValueType, raw: string): PropertyValue {
  if (type === 'bool') return { type, bool: raw === 'true' }
  if (type === 'int') return { type, int: Number(raw) }
  if (type === 'number') return { type, number: Number(raw) }
  return { type, string: raw }
}

export function MappingPreview({ runPreview = previewMapping, loadProfiles = listMappingProfiles, profileRevision = 0 }: { runPreview?: PreviewFunction; loadProfiles?: ProfileLoader; profileRevision?: number }) {
  const [transformType, setTransformType] = useState<MappingTransformType>('scale')
  const [direction, setDirection] = useState<MappingDirection>('forward')
  const [rawValue, setRawValue] = useState('20')
  const [factor, setFactor] = useState(1.8)
  const [offset, setOffset] = useState(32)
  const [minimum, setMinimum] = useState(0)
  const [maximum, setMaximum] = useState(100)
  const [result, setResult] = useState<MappingPreviewResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
	const [profiles, setProfiles] = useState<MappingProfileInfo[]>([])
	const [profileID, setProfileID] = useState('')
	useEffect(() => { let active = true; void loadProfiles().then((items) => { if (active) setProfiles(items) }).catch(() => undefined); return () => { active = false } }, [loadProfiles, profileRevision])
	const selectedProfile = profiles.find((item) => item.id === profileID)
  const activeInputType = selectedProfile ? (direction === 'forward' ? selectedProfile.inputType : selectedProfile.outputType) : (transformType === 'int-number' && direction === 'reverse' ? 'number' : transformInputType(transformType))
  const activeEnumOptions = selectedProfile && activeInputType === 'enum' ? enumOptions(selectedProfile, direction) : []

  const changeTransform = (next: MappingTransformType) => { setTransformType(next); setRawValue(defaultsFor(next)); setResult(null); setError(null) }
  const changeProfile = (id: string) => {
    const item = profiles.find((profile) => profile.id === id)
    const type = item ? (direction === 'forward' ? item.inputType : item.outputType) : transformInputType(transformType)
    setProfileID(id)
    setRawValue(defaultInput(type, item && type === 'enum' ? enumOptions(item, direction) : []))
    setResult(null); setError(null)
  }
  const changeDirection = (next: MappingDirection) => {
    const type = selectedProfile ? (next === 'forward' ? selectedProfile.inputType : selectedProfile.outputType) : (transformType === 'int-number' && next === 'reverse' ? 'number' : transformInputType(transformType))
    setDirection(next)
    setRawValue(defaultInput(type, selectedProfile && type === 'enum' ? enumOptions(selectedProfile, next) : []))
    setResult(null); setError(null)
  }
  const submit = async () => {
		if (selectedProfile) {
			const type = direction === 'forward' ? selectedProfile.inputType : selectedProfile.outputType
			const request: MappingPreviewRequest = { profileId: selectedProfile.id, direction, value: inputValue(type, rawValue) }
			setLoading(true); setError(null)
			try { setResult(await runPreview(request)) } catch (cause) { setResult(null); setError(cause instanceof Error ? cause.message : '映射预览失败') } finally { setLoading(false) }
			return
		}
    let inputType: ValueType = 'number'
    let outputType: ValueType = 'number'
    let value: PropertyValue = { type: 'number', number: Number(rawValue) }
    let transform: MappingTransform = { type: transformType }
    if (transformType === 'invert') {
      inputType = outputType = 'bool'; value = { type: 'bool', bool: rawValue === 'true' }
    } else if (transformType === 'reciprocal' || transformType === 'int-number') {
      transform = { type: transformType }
      if (transformType === 'int-number') {
        inputType = 'int'; outputType = 'number'
        value = direction === 'forward' ? { type: 'int', int: Number(rawValue) } : { type: 'number', number: Number(rawValue) }
      }
    } else if (transformType === 'scale') {
      transform = { type: 'scale', factor, offset }
    } else if (transformType === 'clamp') {
      transform = { type: 'clamp', min: minimum, max: maximum }
    } else if (transformType === 'unit') {
      transform = { type: 'unit', fromUnit: 'celsius', toUnit: 'fahrenheit' }
    } else {
      inputType = outputType = 'enum'; value = { type: 'enum', string: rawValue }; transform = { type: 'enum', values: { off: 'inactive', on: 'active' } }
    }
    const request: MappingPreviewRequest = { profile: { schemaVersion: 1, id: 'console-preview', version: 1, kind: 'capability', inputType, outputType, transforms: [transform] }, direction, value }
    setLoading(true); setError(null)
    try { setResult(await runPreview(request)) } catch (cause) { setResult(null); setError(cause instanceof Error ? cause.message : '映射预览失败') } finally { setLoading(false) }
  }

  return <section className="mapping-preview">
    <div className="config-note"><span>执行方式</span><strong>无状态预览</strong><p>输入不会保存到数据库，也不会写入设备。正向（forward）模拟提供端（Provider）→ 能力 / 目标端（Capability / Target）；反向（reverse）模拟设备控制写回提供端（Provider）。</p></div>
    <div className="mapping-workbench">
      <form className="mapping-form" onSubmit={(event) => { event.preventDefault(); void submit() }}>
		<label>转换配置（Profile）<select aria-label="预览 Profile" value={profileID} onChange={(event) => changeProfile(event.target.value)}><option value="">临时配置（Profile）</option>{profiles.map((item) => <option key={item.id} value={item.id}>{item.id} · 版本（v）{item.version}</option>)}</select></label>
        {!selectedProfile && <label>转换类型（transform）<select aria-label="转换类型" value={transformType} onChange={(event) => changeTransform(event.target.value as MappingTransformType)}>{(['scale', 'reciprocal', 'int-number', 'invert', 'unit', 'enum', 'clamp'] as MappingTransformType[]).map((type) => <option key={type} value={type}>{transformTypeLabel(type)}</option>)}</select></label>}
        <label>方向（direction）<select aria-label="映射方向" value={direction} onChange={(event) => changeDirection(event.target.value as MappingDirection)}><option value="forward">正向（forward）</option><option value="reverse">反向写入（reverse）</option></select></label>
        <label>输入值 · {valueTypeLabel(activeInputType)}
          {activeInputType === 'bool'
            ? <select aria-label="预览输入值" value={rawValue} onChange={(event) => setRawValue(event.target.value)}><option value="true">true</option><option value="false">false</option></select>
            : activeInputType === 'enum' && activeEnumOptions.length > 0
              ? <select aria-label="预览输入值" value={rawValue} onChange={(event) => setRawValue(event.target.value)}>{activeEnumOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select>
              : activeInputType === 'int' || activeInputType === 'number'
                ? <input aria-label="预览输入值" required type="number" step={activeInputType === 'int' ? '1' : 'any'} value={rawValue} onChange={(event) => setRawValue(event.target.value)} />
                : <input aria-label="预览输入值" required value={rawValue} onChange={(event) => setRawValue(event.target.value)} placeholder={activeInputType === 'enum' ? '输入枚举值' : '输入文本'} />}
        </label>
        {!selectedProfile && transformType === 'scale' && <><label>缩放系数<input aria-label="缩放系数" required type="number" step="any" value={factor} onChange={(event) => setFactor(Number(event.target.value))} /></label><label>偏移量<input aria-label="偏移量" required type="number" step="any" value={offset} onChange={(event) => setOffset(Number(event.target.value))} /></label></>}
        {!selectedProfile && transformType === 'clamp' && <><label>最小值<input aria-label="裁剪最小值" required type="number" step="any" value={minimum} onChange={(event) => setMinimum(Number(event.target.value))} /></label><label>最大值<input aria-label="裁剪最大值" required type="number" step="any" value={maximum} onChange={(event) => setMaximum(Number(event.target.value))} /></label></>}
        <button className="add-button" disabled={loading} type="submit">{loading ? '计算中…' : '运行预览'}</button>
        {error && <p className="field-error" role="alert">{error}</p>}
      </form>
      <div className="mapping-result" aria-live="polite">
        {!result ? <div className="empty-state">配置参数后运行预览，结果会展示每一步带类型的值（typed value）。</div> : <><div className="mapping-output"><span>最终输出 · {valueTypeLabel(result.value.type)}</span><strong>{valueText(result.value)}</strong><small>{result.profileId} 版本（v）{result.profileVersion} · {result.direction === 'forward' ? '正向（forward）' : '反向（reverse）'}</small></div><div className="mapping-steps"><h3>转换步骤</h3>{result.steps.map((step) => <div key={`${step.index}-${step.transform}`}><code>{step.index + 1}. {step.transform}</code><span>{step.input ? valueText(step.input) : '缺失（missing）'} → {valueText(step.output)}</span><small>{step.input ? valueTypeLabel(step.input.type) : '空值（null）'} → {valueTypeLabel(step.output.type)}</small></div>)}</div></>}
      </div>
    </div>
  </section>
}
