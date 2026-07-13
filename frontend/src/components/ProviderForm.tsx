import { useState } from 'react'
import type { Provider, ProviderInput } from '../types/provider'

export function ProviderForm({ provider, onCancel, onSave }: { provider: Provider | null; onCancel: () => void; onSave: (input: ProviderInput, editing: boolean) => Promise<void> }) {
  const [id, setID] = useState(provider?.id ?? '')
  const [name, setName] = useState(provider?.name ?? '')
  const [type, setType] = useState(provider?.type ?? 'virtual')
  const [enabled, setEnabled] = useState(provider?.enabled ?? true)
  const [config, setConfig] = useState(JSON.stringify(provider?.config ?? {}, null, 2))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const example = { latencyMs: 0, rejectWrites: false, devices: [{ id: 'living-room-light', name: '客厅灯', type: 'lightbulb', online: true, power: false }, { id: 'living-room-humidity', name: '客厅湿度', type: 'humidity-sensor', online: true, humidity: 56.2 }, { id: 'entry-contact', name: '入户门', type: 'contact-sensor', online: true, contact: false }, { id: 'hall-motion', name: '走廊活动', type: 'motion-sensor', online: true, motion: false }] }
  async function submit(event: React.FormEvent) {
    event.preventDefault(); let parsed: Record<string, unknown>
    try { parsed = JSON.parse(config) as Record<string, unknown>; if (!parsed || Array.isArray(parsed)) throw new Error() } catch { setError('扩展配置必须是 JSON 对象'); return }
    setSaving(true); setError(null); try { await onSave({ id, name, type, enabled, config: parsed }, Boolean(provider)) } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败'); setSaving(false) }
  }
  return <div className="modal-backdrop"><form className="target-form" onSubmit={(event) => void submit(event)}>
    <div className="form-heading"><div><p className="eyebrow">PROVIDER</p><h2>{provider ? '编辑 Provider' : '新建 Provider'}</h2></div><button type="button" onClick={onCancel}>关闭</button></div>
    <div className="form-grid"><label>ID（留空自动生成）<input value={id} disabled={Boolean(provider)} onChange={(event) => setID(event.target.value)} placeholder="virtual-lab" /></label><label>名称<input value={name} onChange={(event) => setName(event.target.value)} placeholder="实验室虚拟设备" /></label><label className="wide">类型<select value={type} disabled={Boolean(provider)} onChange={(event) => setType(event.target.value)}><option value="virtual">Virtual</option></select></label><label className="wide config-editor"><span>扩展配置（JSON）</span><textarea rows={11} value={config} onChange={(event) => setConfig(event.target.value)} spellCheck={false} /><small>支持 switch、lightbulb、outlet、temperature-sensor、humidity-sensor、contact-sensor 和 motion-sensor；状态字段为 power、temperature、humidity、contact、motion。</small><button type="button" className="example-button" onClick={() => setConfig(JSON.stringify(example, null, 2))}>载入传感器示例</button></label></div>
    <label className="enable-row"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />立即启用（无需重启服务）</label>
    {error && <p className="inline-error">{error}</p>}<div className="form-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary" disabled={saving}>{saving ? '应用中…' : '保存并应用'}</button></div>
  </form></div>
}
