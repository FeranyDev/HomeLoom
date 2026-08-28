import { useMemo, useState } from 'react'
import type { Device, DeviceType } from '../types/device'
import { builtInDeviceTypes } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { deviceTypeLabel } from '../presentationLabels'
import { ProviderDeviceAddFlow } from './ProviderDeviceAddFlow'

type VirtualDeviceEntry = Record<string, unknown>

const deviceIDPattern = /^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/
const virtualDeviceTypes: readonly string[] = [...builtInDeviceTypes, 'camera']

function configuredDevices(provider: Provider): VirtualDeviceEntry[] {
	return Array.isArray(provider.config.devices)
		? provider.config.devices.filter((item): item is VirtualDeviceEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item)))
		: []
}

function defaultDevice(type: string, index = 1): VirtualDeviceEntry {
	const base: VirtualDeviceEntry = { id: `virtual-${type}-${index}`, name: deviceTypeLabel(type as DeviceType), type, availability: 'online', online: true }
	switch (type) {
	case 'switch': return { ...base, name: '客厅开关', power: false }
	case 'lightbulb': return { ...base, name: '客厅灯', power: true, brightness: 80, colorTemperature: 250, hue: 35, saturation: 45 }
	case 'outlet': return { ...base, name: '书房插座', power: false, inUse: false, currentPower: 0, energy: 0 }
	case 'temperature-sensor': return { ...base, name: '客厅温度', temperature: 23.6, batteryLevel: 91, lowBattery: false }
	case 'humidity-sensor': return { ...base, name: '客厅湿度', humidity: 56.2, batteryLevel: 90, lowBattery: false }
	case 'temperature-humidity-sensor': return { ...base, name: '客厅温湿度', temperature: 23.6, humidity: 56.2, batteryLevel: 87, lowBattery: false }
	case 'contact-sensor': return { ...base, name: '入户门', contact: false, batteryLevel: 88, lowBattery: false, tampered: false }
	case 'motion-sensor': return { ...base, name: '走廊活动', motion: false, batteryLevel: 84, lowBattery: false, tampered: false }
	case 'fan': return { ...base, name: '卧室风扇', active: false, speed: 35, mode: 'manual', swingMode: true, direction: 'clockwise', controlLock: false }
	case 'air-purifier': return { ...base, name: '客厅净化器', active: true, speed: 60, mode: 'auto', swingMode: false, controlLock: false, airQuality: 'good', pm25: 12, voc: 80, filterLife: 82, filterChange: false }
	case 'window-covering': return { ...base, name: '南窗帘', position: 50, obstruction: false }
	default: return base
	}
}

function numericValue(entry: VirtualDeviceEntry, field: string, fallback: number): string | number {
	const value = entry[field]
	return value === undefined || value === null ? fallback : String(value)
}

export function VirtualDeviceManager({ provider, devices, onClose, onSave }: {
	provider: Provider
	devices: Device[]
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
}) {
	const initialEntries = useMemo(() => configuredDevices(provider), [provider])
	const [entries, setEntries] = useState(initialEntries)
	const [entriesJSON, setEntriesJSON] = useState(JSON.stringify(initialEntries, null, 2))
	const [draft, setDraft] = useState<VirtualDeviceEntry>(() => defaultDevice('switch', initialEntries.length + 1))
	const [editingID, setEditingID] = useState<string | null>(null)
	const [showEditor, setShowEditor] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const connected = provider.enabled && provider.status === 'running'
	const published = useMemo(() => new Map(devices.map((item) => [item.id, item])), [devices])

	function update(field: string, value: unknown) {
		setDraft((current) => ({ ...current, [field]: value }))
	}

	function replaceEntries(next: VirtualDeviceEntry[]) {
		setEntries(next)
		setEntriesJSON(JSON.stringify(next, null, 2))
	}

	function resetEditor() {
		setDraft(defaultDevice('switch', entries.length + 1))
		setEditingID(null)
		setShowEditor(false)
	}

	function beginAdd() {
		setDraft(defaultDevice('switch', entries.length + 1))
		setEditingID(null)
		setShowEditor(true)
		setError(null)
	}

	function beginEdit(item: VirtualDeviceEntry) {
		setDraft({ ...item })
		setEditingID(String(item.id))
		setShowEditor(true)
		setError(null)
	}

	function changeType(type: string) {
		setDraft((current) => ({
			...defaultDevice(type, entries.length + 1),
			id: current.id,
			name: current.name,
			availability: current.availability ?? 'online',
			online: current.online ?? true,
		}))
	}

	function changeAvailability(value: string) {
		update('availability', value)
		update('online', value === 'online')
	}

	function applyDraft() {
		const id = String(draft.id ?? '').trim().toLowerCase()
		const name = String(draft.name ?? '').trim()
		const type = String(draft.type ?? '').trim()
		if (!deviceIDPattern.test(id)) {
			setError('设备 ID 必须是 1–64 位小写稳定标识，可使用数字、点、下划线和连字符。')
			return
		}
		if (!name) {
			setError('请输入虚拟设备名称。')
			return
		}
		if (!virtualDeviceTypes.includes(type)) {
			setError('请选择一个受支持的统一设备模型。')
			return
		}
		if (entries.some((item) => String(item.id) === id && String(item.id) !== editingID)) {
			setError(`设备 ID“${id}”已经存在。`)
			return
		}
		const availability = String(draft.availability ?? (draft.online === false ? 'offline' : 'online'))
		const normalized = { ...draft, id, name, type, availability, online: availability === 'online' }
		const next = editingID
			? entries.map((item) => String(item.id) === editingID ? normalized : item)
			: [...entries, normalized]
		replaceEntries(next)
		setShowEditor(false)
		setEditingID(null)
		setError(null)
		setResult(editingID ? `已更新虚拟设备“${name}”，保存后生效。` : `已添加虚拟设备“${name}”，保存后生效。`)
	}

	async function save() {
		let parsed: unknown
		try { parsed = JSON.parse(entriesJSON) } catch { setError('虚拟子设备配置必须是有效 JSON'); return }
		if (!Array.isArray(parsed)) { setError('虚拟子设备配置必须是 JSON 数组'); return }
		setSaving(true)
		setError(null)
		setResult(null)
		try {
			await onSave({ id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled, config: { ...provider.config, devices: parsed } }, true)
			const next = parsed.filter((item): item is VirtualDeviceEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item)))
			setEntries(next)
			setEntriesJSON(JSON.stringify(next, null, 2))
			setResult(`已保存 ${next.length} 台虚拟子设备；Provider 运行目录已实时应用。`)
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '保存虚拟子设备失败')
		} finally {
			setSaving(false)
		}
	}

	function numberField(field: string, label: string, fallback: number, min?: number, max?: number, step = 0.1) {
		return <label>{label}<input aria-label={label} type="number" min={min} max={max} step={step} value={numericValue(draft, field, fallback)} onChange={(event) => update(field, event.target.value === '' ? '' : Number(event.target.value))} /></label>
	}

	function booleanField(field: string, label: string, fallback = false) {
		return <label className="enable-row"><input aria-label={label} type="checkbox" checked={draft[field] === undefined ? fallback : Boolean(draft[field])} onChange={(event) => update(field, event.target.checked)} />{label}</label>
	}

	function typeSpecificFields() {
		switch (String(draft.type)) {
		case 'switch': return <>{booleanField('power', '初始开关状态')}</>
		case 'lightbulb': return <>{booleanField('power', '初始开关状态', true)}{numberField('brightness', '初始亮度', 80, 0, 100)}{numberField('colorTemperature', '初始色温', 250, 140, 500, 1)}{numberField('hue', '初始色相', 35, 0, 360)}{numberField('saturation', '初始饱和度', 45, 0, 100)}</>
		case 'outlet': return <>{booleanField('power', '初始开关状态')}{booleanField('inUse', '初始使用状态')}{numberField('currentPower', '初始功率', 0, 0)}{numberField('energy', '累计电量', 0, 0)}</>
		case 'temperature-sensor': return <>{numberField('temperature', '初始温度', 23.6, -100, 200)}{numberField('batteryLevel', '电池电量', 91, 0, 100, 1)}{booleanField('lowBattery', '低电量提示')}</>
		case 'humidity-sensor': return <>{numberField('humidity', '初始湿度', 56.2, 0, 100)}{numberField('batteryLevel', '电池电量', 90, 0, 100, 1)}{booleanField('lowBattery', '低电量提示')}</>
		case 'temperature-humidity-sensor': return <>{numberField('temperature', '初始温度', 23.6, -100, 200)}{numberField('humidity', '初始湿度', 56.2, 0, 100)}{numberField('batteryLevel', '电池电量', 87, 0, 100, 1)}{booleanField('lowBattery', '低电量提示')}</>
		case 'contact-sensor': return <>{booleanField('contact', '初始接触状态')}{numberField('batteryLevel', '电池电量', 88, 0, 100, 1)}{booleanField('lowBattery', '低电量提示')}{booleanField('tampered', '防拆提示')}</>
		case 'motion-sensor': return <>{booleanField('motion', '初始活动状态')}{numberField('batteryLevel', '电池电量', 84, 0, 100, 1)}{booleanField('lowBattery', '低电量提示')}{booleanField('tampered', '防拆提示')}</>
		case 'fan': return <>{booleanField('active', '初始运行状态')}{numberField('speed', '初始速度', 35, 0, 100)}<label>初始模式<select aria-label="初始模式" value={String(draft.mode ?? 'manual')} onChange={(event) => update('mode', event.target.value)}><option value="manual">手动</option><option value="auto">自动</option></select></label>{booleanField('swingMode', '摆风')}{booleanField('controlLock', '锁定物理控制')}</>
		case 'air-purifier': return <>{booleanField('active', '初始运行状态', true)}{numberField('speed', '初始速度', 60, 0, 100)}<label>初始模式<select aria-label="初始模式" value={String(draft.mode ?? 'auto')} onChange={(event) => update('mode', event.target.value)}><option value="manual">手动</option><option value="auto">自动</option></select></label><label>空气质量<select aria-label="初始空气质量" value={String(draft.airQuality ?? 'good')} onChange={(event) => update('airQuality', event.target.value)}><option value="unknown">未知</option><option value="excellent">优秀</option><option value="good">良好</option><option value="fair">一般</option><option value="inferior">较差</option><option value="poor">差</option></select></label>{numberField('pm25', 'PM2.5', 12, 0)}{numberField('voc', 'VOC', 80, 0)}{numberField('filterLife', '滤芯寿命', 82, 0, 100)}{booleanField('filterChange', '提示更换滤芯')}</>
		case 'window-covering': return <>{numberField('position', '初始位置', 50, 0, 100, 1)}{booleanField('obstruction', '障碍物提示')}</>
		case 'television': return <>{booleanField('active', '初始电源状态', true)}{numberField('volume', '初始音量', 35, 0, 100, 1)}</>
		default: return <small className="virtual-device-editor__hint">该模型使用统一契约默认值；如需调整契约字段，可在下方高级 JSON 中补充。</small>
		}
	}

	return <section className="xiaomi-device-manager virtual-device-manager">
		<header><div><p className="eyebrow">VIRTUAL PROVIDER · CHILD DEVICES</p><h3>{provider.name} · 虚拟子设备</h3><p>Provider 只负责模拟运行时；每个虚拟设备在这里独立添加、编辑或移除。</p></div><button type="button" onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? 'Virtual Provider 运行中' : 'Virtual Provider 未运行'}</strong><small>{provider.id} · {entries.length} 台虚拟子设备</small></div><button type="button" disabled={!connected || showEditor} onClick={beginAdd}>添加虚拟设备</button></div>
		<ProviderDeviceAddFlow source="手动创建模拟设备；无需外部账号或局域网扫描。" model="选择统一模型，并填写该模型的初始状态。" configuration="设备先加入草稿，最后统一点击“保存设备并应用”。" />
		{!connected && <p className="inline-error" role="alert">请先启用 Virtual Provider；Provider 进入 running 后才能添加并实时应用虚拟子设备。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success">{result}</p>}
		{showEditor && <div className="xiaomi-device-binding virtual-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>{editingID ? '编辑虚拟设备草稿' : '添加虚拟设备草稿'}</strong><small>子设备归属于 {provider.id}；保存后会通过 Provider live reconfigure 实时应用。</small></div></div><div className="form-grid">
			<label>设备 ID<input aria-label="虚拟设备 ID" disabled={Boolean(editingID)} value={String(draft.id ?? '')} onChange={(event) => update('id', event.target.value)} placeholder="living-room-switch" /></label>
			<label>名称<input aria-label="虚拟设备名称" value={String(draft.name ?? '')} onChange={(event) => update('name', event.target.value)} placeholder="客厅开关" /></label>
			<label>统一模型<select aria-label="虚拟设备模型" disabled={Boolean(editingID)} value={String(draft.type ?? 'switch')} onChange={(event) => changeType(event.target.value)}>{virtualDeviceTypes.map((type) => <option value={type} key={type}>{deviceTypeLabel(type as DeviceType)}</option>)}</select></label>
			<label>初始可用性<select aria-label="虚拟设备可用性" value={String(draft.availability ?? 'online')} onChange={(event) => changeAvailability(event.target.value)}><option value="online">在线</option><option value="offline">离线</option><option value="unknown">未知</option></select></label>
			<div className="virtual-device-fields wide">{typeSpecificFields()}</div>
		</div><div className="form-actions"><button type="button" onClick={resetEditor}>取消</button><button type="button" className="primary" onClick={applyDraft}>{editingID ? '更新草稿' : '加入草稿'}</button></div></div>}
		<div className="provider-device-list virtual-device-list"><div className="command-heading"><h3>已配置虚拟设备</h3><span>{entries.length} 台</span></div>{entries.length === 0 ? <p>尚未添加虚拟设备。先保持 Provider 运行，再点击“添加虚拟设备”。</p> : entries.map((entry) => { const current = published.get(String(entry.id)); return <div key={String(entry.id)}><span className={`status-dot ${current?.availability === 'online' ? 'is-online' : ''}`} /><strong>{String(current?.name || entry.name || entry.id)}</strong><code>{String(entry.id)}</code><small>{deviceTypeLabel(String(entry.type) as DeviceType)} · {String(entry.availability || (entry.online === false ? 'offline' : 'online'))}</small><button type="button" onClick={() => beginEdit(entry)}>编辑</button><button type="button" className="is-danger" onClick={() => replaceEntries(entries.filter((item) => String(item.id) !== String(entry.id)))}>移除</button></div> })}</div>
		<details><summary>虚拟子设备高级 JSON</summary><textarea aria-label="虚拟子设备 JSON" rows={14} value={entriesJSON} onChange={(event) => setEntriesJSON(event.target.value)} spellCheck={false} /><small>用于导入历史配置或调整可视化表单未展示的字段；保存时以后端严格校验结果为准。</small></details>
		<div className="form-actions"><button type="button" onClick={onClose}>取消</button><button type="button" className="primary" disabled={!connected || saving} onClick={() => void save()}>{saving ? '应用中…' : '保存设备并应用'}</button></div>
	</section>
}
