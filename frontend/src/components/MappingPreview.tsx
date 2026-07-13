import { useEffect, useState } from 'react'
import { listMappingProfiles, previewMapping } from '../api/mapping'
import type { PropertyValue, ValueType } from '../types/device'
import type { MappingDirection, MappingPreviewRequest, MappingPreviewResult, MappingProfileInfo, MappingTransform, MappingTransformType } from '../types/mapping'

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

  const changeTransform = (next: MappingTransformType) => { setTransformType(next); setRawValue(defaultsFor(next)); setResult(null); setError(null) }
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
    <div className="config-note"><span>执行方式</span><strong>无状态预览</strong><p>输入不会保存到数据库，也不会写入设备。正向模拟 Provider → Capability/Target；反向模拟设备控制写回 Provider。</p></div>
    <div className="mapping-workbench">
      <form className="mapping-form" onSubmit={(event) => { event.preventDefault(); void submit() }}>
		<label>Profile<select aria-label="预览 Profile" value={profileID} onChange={(event) => { const id = event.target.value; setProfileID(id); const item = profiles.find((profile) => profile.id === id); setRawValue(item && (item.inputType === 'bool' || item.outputType === 'bool') ? 'true' : '20'); setResult(null) }}><option value="">临时 Profile</option>{profiles.map((item) => <option key={item.id} value={item.id}>{item.id} · v{item.version}</option>)}</select></label>
        {!selectedProfile && <label>转换类型<select aria-label="转换类型" value={transformType} onChange={(event) => changeTransform(event.target.value as MappingTransformType)}><option value="scale">数值缩放与偏移</option><option value="invert">布尔反转</option><option value="unit">摄氏转华氏</option><option value="enum">枚举映射</option><option value="clamp">数值范围裁剪</option></select></label>}
        <label>方向<select aria-label="映射方向" value={direction} onChange={(event) => { setDirection(event.target.value as MappingDirection); setResult(null) }}><option value="forward">正向</option><option value="reverse">反向写入</option></select></label>
		{selectedProfile ? ((direction === 'forward' ? selectedProfile.inputType : selectedProfile.outputType) === 'bool' ? <label>输入值<select aria-label="预览输入值" value={rawValue} onChange={(event) => setRawValue(event.target.value)}><option value="true">true</option><option value="false">false</option></select></label> : <label>输入值<input aria-label="预览输入值" value={rawValue} onChange={(event) => setRawValue(event.target.value)} /></label>) : transformType === 'invert' ? <label>输入值<select aria-label="预览输入值" value={rawValue} onChange={(event) => setRawValue(event.target.value)}><option value="true">true</option><option value="false">false</option></select></label> : transformType === 'enum' ? <label>输入值<input aria-label="预览输入值" value={rawValue} onChange={(event) => setRawValue(event.target.value)} placeholder={direction === 'forward' ? 'off / on' : 'inactive / active'} /></label> : <label>输入数值<input aria-label="预览输入值" required type="number" step="any" value={rawValue} onChange={(event) => setRawValue(event.target.value)} /></label>}
        {!selectedProfile && transformType === 'scale' && <><label>缩放系数<input aria-label="缩放系数" required type="number" step="any" value={factor} onChange={(event) => setFactor(Number(event.target.value))} /></label><label>偏移量<input aria-label="偏移量" required type="number" step="any" value={offset} onChange={(event) => setOffset(Number(event.target.value))} /></label></>}
        {!selectedProfile && transformType === 'clamp' && <><label>最小值<input aria-label="裁剪最小值" required type="number" step="any" value={minimum} onChange={(event) => setMinimum(Number(event.target.value))} /></label><label>最大值<input aria-label="裁剪最大值" required type="number" step="any" value={maximum} onChange={(event) => setMaximum(Number(event.target.value))} /></label></>}
        <button className="add-button" disabled={loading} type="submit">{loading ? '计算中…' : '运行预览'}</button>
        {error && <p className="field-error" role="alert">{error}</p>}
      </form>
      <div className="mapping-result" aria-live="polite">
        {!result ? <div className="empty-state">配置参数后运行预览，结果会展示每一步 typed value。</div> : <><div className="mapping-output"><span>最终输出 · {result.value.type}</span><strong>{valueText(result.value)}</strong><small>{result.profileId} v{result.profileVersion} · {result.direction}</small></div><div className="mapping-steps"><h3>转换步骤</h3>{result.steps.map((step) => <div key={`${step.index}-${step.transform}`}><code>{step.index + 1}. {step.transform}</code><span>{step.input ? valueText(step.input) : 'missing'} → {valueText(step.output)}</span><small>{step.input?.type ?? 'null'} → {step.output.type}</small></div>)}</div></>}
      </div>
    </div>
  </section>
}
