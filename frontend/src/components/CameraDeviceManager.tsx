import { useMemo, useState } from 'react'
import { discoverXiaomiDevices, type XiaomiHubDevice } from '../api/xiaomi'
import type { Device } from '../types/device'
import type { Provider, ProviderInput } from '../types/provider'
import { inferXiaomiDeviceType, stableXiaomiControlID, stableXiaomiID } from '../xiaomiMappings'

type CameraEntry = Record<string, unknown>
type CameraScanResult = XiaomiHubDevice & {
	sourceProviderId: string
	sourceProviderName: string
	sourceProviderType: string
}
type CameraControlOption = {
	providerRef: string
	deviceId: string
	providerName: string
	deviceName: string
	deviceType?: string
	online?: boolean
}

const defaultProfile = {
	schemaVersion: 1, id: 'main', name: '主码流',
	width: 1920, height: 1080, fps: 25,
	videoCodec: 'h264', audioCodec: 'aac', bitrate: 2_000_000,
}

const xiaomiSubtypes = [
	['auto', '自动选择'],
	['sd', '标清'],
	['hd', '高清（默认）'],
	['0', '厂商码流 0'],
	['1', '厂商码流 1'],
	['2', '厂商码流 2'],
	['3', '厂商码流 3'],
	['4', '厂商码流 4'],
	['5', '厂商码流 5'],
] as const

function cameraEntries(provider: Provider): CameraEntry[] {
	return Array.isArray(provider.config.cameras)
		? provider.config.cameras.filter((item): item is CameraEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item)))
		: []
}

function emptyDraft(): CameraEntry {
	return {
		id: '', name: '', driver: 'rtsp', connectionMode: 'on_demand', enabled: true, profiles: [defaultProfile],
		rtsp: { host: '', port: 554, path: '', authType: 'basic', username: '', password: '' },
	}
}

function objectValue(value: unknown): Record<string, unknown> {
	return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

export function CameraDeviceManager({ provider, providers = [], devices = [], onClose, onSave }: {
	provider: Provider
	providers?: Provider[]
	devices?: Device[]
	onClose: () => void
	onSave: (input: ProviderInput, editing: boolean) => Promise<void>
}) {
	const initialEntries = useMemo(() => cameraEntries(provider), [provider])
	const [entries, setEntries] = useState(initialEntries)
	const [entriesJSON, setEntriesJSON] = useState(JSON.stringify(initialEntries, null, 2))
	const [draft, setDraft] = useState<CameraEntry>(emptyDraft)
	const [editingID, setEditingID] = useState<string | null>(null)
	const [showEditor, setShowEditor] = useState(false)
	const [scanResults, setScanResults] = useState<CameraScanResult[]>([])
	const [scanning, setScanning] = useState(false)
	const [saving, setSaving] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [result, setResult] = useState<string | null>(null)
	const connected = provider.enabled && provider.status === 'running'
	const credentialProviders = providers.filter((item) => item.type === 'xiaomi-miot-cloud' && item.enabled && item.status === 'running')
	const controlOptions = useMemo(() => {
		const options = new Map<string, CameraControlOption>()
		for (const source of providers.filter((item) => item.type === 'xiaomi' || item.type === 'xiaomi-miot-cloud')) {
			const configured = Array.isArray(source.config.devices) ? source.config.devices : []
			for (const raw of configured) {
				if (!raw || typeof raw !== 'object' || Array.isArray(raw)) continue
				const item = raw as Record<string, unknown>
				const did = String(item.did ?? '').trim()
				const isCamera = String(item.type ?? '') === 'camera' || inferXiaomiDeviceType({
					did,
					name: String(item.name ?? did),
					model: String(item.model ?? ''),
					specType: String(item.specType ?? ''),
				}) === 'camera'
				if (!isCamera) continue
				const configuredID = String(item.id ?? '').trim()
				const deviceId = source.type === 'xiaomi' && did && configuredID === stableXiaomiID(did)
					? stableXiaomiControlID(did)
					: configuredID || (did
					? source.type === 'xiaomi' ? stableXiaomiControlID(did) : stableXiaomiID(did).replace(/^xiaomi-/, 'xiaomi-miot-')
					: '')
				if (!deviceId) continue
				options.set(`${source.id}\u0000${deviceId}`, {
					providerRef: source.id,
					deviceId,
					providerName: source.name,
					deviceName: String(item.name ?? item.did ?? deviceId),
					deviceType: String(item.type ?? ''),
				})
			}
		}
		for (const item of devices) {
			if (item.removed || item.type !== 'camera' || !providers.some((source) =>
				source.id === item.providerId && (source.type === 'xiaomi' || source.type === 'xiaomi-miot-cloud'))) continue
			const source = providers.find((candidate) => candidate.id === item.providerId)!
			const key = `${item.providerId}\u0000${item.id}`
			options.set(key, {
				...options.get(key),
				providerRef: item.providerId,
				deviceId: item.id,
				providerName: source.name,
				deviceName: item.name,
				deviceType: item.type,
				online: item.online,
			})
		}
		return [...options.values()].sort((left, right) =>
			left.providerName.localeCompare(right.providerName) || left.deviceName.localeCompare(right.deviceName))
	}, [devices, providers])
	const driver = String(draft.driver ?? 'rtsp')
	const rtsp = objectValue(draft.rtsp)
	const onvif = objectValue(draft.onvif)
	const xiaomi = objectValue(draft.xiaomi)
	const control = objectValue(draft.control)
	const controlValue = String(control.providerRef ?? '') && String(control.deviceId ?? '')
		? `${String(control.providerRef)}\u0000${String(control.deviceId)}`
		: ''
	const selectedControlIsMissing = Boolean(controlValue && !controlOptions.some((item) =>
		item.providerRef === control.providerRef && item.deviceId === control.deviceId))
	const connectionMode = String(draft.connectionMode ?? 'on_demand')

	function replaceEntries(next: CameraEntry[]) {
		setEntries(next)
		setEntriesJSON(JSON.stringify(next, null, 2))
	}

	function updateDraft(key: string, value: unknown) {
		setDraft((current) => ({ ...current, [key]: value }))
	}

	function updateNested(key: 'rtsp' | 'onvif' | 'xiaomi', field: string, value: unknown) {
		setDraft((current) => ({ ...current, [key]: { ...objectValue(current[key]), [field]: value } }))
	}

	function updateControl(value: string) {
		if (!value) {
			setDraft((current) => {
				const next = { ...current }
				delete next.control
				return next
			})
			return
		}
		const separator = value.indexOf('\u0000')
		setDraft((current) => ({
			...current,
			control: { providerRef: value.slice(0, separator), deviceId: value.slice(separator + 1) },
		}))
	}

	function beginAdd() {
		setDraft(emptyDraft())
		setEditingID(null)
		setShowEditor(true)
		setError(null)
	}

	function beginEdit(entry: CameraEntry) {
		setDraft(structuredClone(entry))
		setEditingID(String(entry.id ?? ''))
		setShowEditor(true)
		setError(null)
	}

	async function scanCameras() {
		const sources = providers.filter((item) =>
			(item.type === 'xiaomi' || item.type === 'xiaomi-miot-cloud') &&
			item.enabled && item.status === 'running')
		if (sources.length === 0) {
			setError('没有可用的 Xiaomi 或 Xiaomi MIoT Cloud 账号 Provider；请先配置并启用账号目录。')
			setScanResults([])
			return
		}
		setScanning(true); setError(null); setResult(null)
		try {
			const settled = await Promise.allSettled(sources.map(async (source) => {
				const items = await discoverXiaomiDevices(source.id, source.type)
				return items.filter((item) => inferXiaomiDeviceType(item) === 'camera').map((item) => ({
					...item,
					sourceProviderId: source.id,
					sourceProviderName: source.name,
					sourceProviderType: source.type,
				}))
			}))
			const failures = settled.filter((item) => item.status === 'rejected')
			const byDID = new Map<string, CameraScanResult>()
			for (const outcome of settled) {
				if (outcome.status !== 'fulfilled') continue
				for (const item of outcome.value) {
					if (!byDID.has(item.did)) byDID.set(item.did, item)
				}
			}
			const found = [...byDID.values()]
			setScanResults(found)
			setResult(found.length
				? `已从 ${sources.length} 个账号目录发现 ${found.length} 台摄像头${failures.length ? `；${failures.length} 个目录读取失败` : ''}。`
				: `已扫描 ${sources.length} 个账号目录，没有发现摄像头${failures.length ? `；${failures.length} 个目录读取失败` : ''}。`)
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '扫描摄像头失败')
		} finally {
			setScanning(false)
		}
	}

	function configureScannedCamera(item: CameraScanResult) {
		const id = stableXiaomiID(item.did)
		setDraft({
			id,
			name: item.name || item.did,
			driver: 'xiaomi-miss',
			connectionMode: 'on_demand',
			enabled: true,
			homeId: item.homeId ?? '',
			home: item.homeName ?? '',
			roomId: item.roomId ?? '',
			room: item.roomName ?? '',
			profiles: [defaultProfile],
			xiaomi: {
				credentialProviderRef: item.sourceProviderType === 'xiaomi-miot-cloud' ? item.sourceProviderId : '',
				region: String(providers.find((source) => source.id === item.sourceProviderId)?.config.region ?? 'cn'),
				username: '', password: '', passToken: '',
				did: item.did, model: item.model ?? '', localIp: item.localIp ?? '',
				subtype: 'hd',
				channel: 1,
			},
		})
		setEditingID(null)
		setShowEditor(true)
		setError(null)
		setResult(item.sourceProviderType === 'xiaomi-miot-cloud'
			? `已载入“${item.name || item.did}”并引用账号 Provider“${item.sourceProviderName}”；Token 将在后端取流授权时复用。`
			: `已载入“${item.name || item.did}”的目录信息；请核对局域网 IP，并选择可复用的 MIoT Cloud 账号或填写独立凭据。`)
	}

	function linkScannedCredential(entry: CameraEntry, item: CameraScanResult) {
		const next = entries.map((current) => current === entry ? {
			...current,
			xiaomi: { ...objectValue(current.xiaomi), credentialProviderRef: item.sourceProviderId },
		} : current)
		replaceEntries(next)
		setResult(`已将“${String(entry.name || entry.id)}”关联到账号 Provider“${item.sourceProviderName}”；保存子设备后生效。`)
		setError(null)
	}

	function applyDraft() {
		const id = String(draft.id ?? '').trim()
		const name = String(draft.name ?? '').trim()
		if (!id || !/^[A-Za-z0-9_-]+$/.test(id)) { setError('摄像头 ID 只能包含字母、数字、下划线和连字符'); return }
		if (!name) { setError('请输入摄像头名称'); return }
		if (entries.some((entry) => String(entry.id) === id && String(entry.id) !== editingID)) { setError(`摄像头 ID“${id}”已存在`); return }
		if (driver === 'rtsp' && (!String(rtsp.host ?? '').trim() || !String(rtsp.path ?? '').trim())) { setError('RTSP 摄像头需要 Host 和 Path'); return }
		if (driver === 'onvif' && (!String(onvif.host ?? '').trim() || !String(onvif.username ?? '').trim() || !String(onvif.password ?? '').trim())) { setError('ONVIF 摄像头需要 Host、用户名和密码'); return }
		if (driver === 'xiaomi-miss' && (!String(xiaomi.did ?? '').trim() || !String(xiaomi.model ?? '').trim() || !String(xiaomi.localIp ?? '').trim())) { setError('小米 MISS 摄像头需要 DID、型号和局域网 IP'); return }
		const normalized: CameraEntry = {
			...draft, id, name, driver, connectionMode, enabled: draft.enabled !== false,
			profiles: Array.isArray(draft.profiles) && draft.profiles.length ? draft.profiles : [defaultProfile],
		}
		const next = editingID
			? entries.map((entry) => String(entry.id) === editingID ? normalized : entry)
			: [...entries, normalized]
		replaceEntries(next)
		setShowEditor(false)
		setEditingID(null)
		setResult(editingID ? `已更新子设备“${name}”，保存后生效。` : `已添加子设备“${name}”，保存后生效。`)
		setError(null)
	}

	async function save() {
		let parsed: unknown
		try { parsed = JSON.parse(entriesJSON) } catch { setError('摄像头子设备配置必须是有效 JSON'); return }
		if (!Array.isArray(parsed)) { setError('摄像头子设备配置必须是 JSON 数组'); return }
		setSaving(true); setError(null); setResult(null)
		try {
			await onSave({
				id: provider.id, name: provider.name, type: provider.type, enabled: provider.enabled,
				config: { ...provider.config, cameras: parsed },
			}, true)
			const next = parsed.filter((item): item is CameraEntry => Boolean(item && typeof item === 'object' && !Array.isArray(item)))
			setEntries(next)
			setResult(`已保存 ${next.length} 台摄像头；媒体目录和 Camera Kernel 已实时应用。`)
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : '保存摄像头子设备失败')
		} finally {
			setSaving(false)
		}
	}

	return <section className="xiaomi-device-manager camera-device-manager">
		<header><div><p className="eyebrow">CAMERA PROVIDER · CHILD DEVICES</p><h3>{provider.name} · 摄像头子设备</h3><p>Provider 只负责媒体运行时边界；每台摄像头在这里独立添加、编辑或移除。</p></div><button onClick={onClose}>返回 Provider</button></header>
		<div className="xiaomi-device-manager__status"><span className={`status-dot ${connected ? 'is-online' : ''}`} /><div><strong>{connected ? 'Camera Provider 运行中 · Camera Kernel 已启用' : 'Camera Provider 未运行 · Camera Kernel 未启用'}</strong><small>{provider.id} · {entries.length} 台摄像头子设备</small></div><button type="button" disabled={!connected || showEditor || scanning} onClick={() => void scanCameras()}>{scanning ? '扫描中…' : '扫描摄像头'}</button><button type="button" disabled={!connected || showEditor || scanning} onClick={beginAdd}>手动添加</button></div>
		{!connected && <p className="inline-error" role="alert">请先启用 Camera Provider；Provider 进入 running 后才能添加并实时应用摄像头。</p>}
		{error && <p className="inline-error" role="alert">{error}</p>}{result && <p className="test-success">{result}</p>}
		{scanResults.length > 0 && <div className="xiaomi-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>扫描到的摄像头</strong><small>来自账号 Provider 的只读设备目录；加入后由当前 Camera Provider 独立管理和发布。</small></div><span>{scanResults.length} 台</span></div><div className="xiaomi-hub-device-list">{scanResults.map((item) => {
			const existing = entries.find((entry) => String(objectValue(entry.xiaomi).did ?? '') === item.did)
			const linked = Boolean(existing && String(objectValue(existing.xiaomi).credentialProviderRef ?? ''))
			const canLink = Boolean(existing && !linked && item.sourceProviderType === 'xiaomi-miot-cloud')
			return <article key={`${item.sourceProviderId}:${item.did}`}><div><strong>{item.name || item.did}</strong><small>{item.homeName || '未知家庭'} / {item.roomName || '未分配房间'} · {item.model || '型号未知'}</small><small>{item.localIp ? `局域网 IP：${item.localIp}` : '未取得局域网 IP，需要手动填写'}</small><code>{item.did}</code><small>目录来源：{item.sourceProviderName}</small></div><button type="button" disabled={Boolean(existing && !canLink) || showEditor} onClick={() => canLink ? linkScannedCredential(existing!, item) : configureScannedCamera(item)}>{linked ? '已关联账号' : canLink ? '关联账号认证' : existing ? '已添加' : '配置并加入'}</button></article>
		})}</div></div>}
		{showEditor && <div className="xiaomi-device-binding"><div className="xiaomi-device-binding__heading"><div><strong>{editingID ? '编辑摄像头' : '添加摄像头'}</strong><small>子设备归属于 {provider.id}，不会自动发布到 HomeKit 或 Matter。</small></div></div>
			<div className="form-grid">
				<label>摄像头 ID<input aria-label="摄像头 ID" disabled={Boolean(editingID)} value={String(draft.id ?? '')} onChange={(event) => updateDraft('id', event.target.value)} placeholder="front-door-camera" /></label>
				<label>名称<input aria-label="摄像头名称" value={String(draft.name ?? '')} onChange={(event) => updateDraft('name', event.target.value)} placeholder="门口摄像头" /></label>
				<label>驱动<select aria-label="摄像头驱动" value={driver} onChange={(event) => {
					const next = event.target.value
					setDraft((current) => next === 'rtsp'
						? { ...current, driver: next, rtsp: objectValue(current.rtsp).host ? current.rtsp : { host: '', port: 554, path: '', authType: 'basic', username: '', password: '' }, onvif: undefined, xiaomi: undefined }
						: next === 'onvif'
							? { ...current, driver: next, onvif: objectValue(current.onvif).host ? current.onvif : { host: '', port: 80, profile: '', username: '', password: '' }, rtsp: undefined, xiaomi: undefined }
							: { ...current, driver: next, xiaomi: objectValue(current.xiaomi).did ? current.xiaomi : { region: 'cn', username: '', password: '', passToken: '', did: '', model: '', localIp: '', subtype: 'hd', channel: 1 }, rtsp: undefined, onvif: undefined })
				}}><option value="rtsp">RTSP</option><option value="onvif">ONVIF</option><option value="xiaomi-miss">Xiaomi MISS</option></select></label>
				<label className="wide">连接模式<select aria-label="摄像头连接模式" value={connectionMode} onChange={(event) => updateDraft('connectionMode', event.target.value)}>
					<option value="on_demand">按需连接：观看时才连接，资源占用最低</option>
					<option value="preload">自动预连接：保持上游视频连接，首次打开更快</option>
					<option value="always_on">长连接：保持 H.264/音频输出热状态，打开最快</option>
				</select><small>预连接会占用摄像头会话；长连接还会持续占用转码资源。</small></label>
				<label className="enable-row"><input type="checkbox" checked={draft.enabled !== false} onChange={(event) => updateDraft('enabled', event.target.checked)} />启用子设备</label>
				<label className="wide">控制来源（可选）
					<select aria-label="摄像头控制来源" value={controlValue} onChange={(event) => updateControl(event.target.value)}>
						<option value="">不合并控制能力（仅视频）</option>
						{selectedControlIsMissing && <option value={controlValue}>当前绑定暂不可用：{String(control.providerRef)} / {String(control.deviceId)}</option>}
						{controlOptions.map((item) => <option key={`${item.providerRef}:${item.deviceId}`} value={`${item.providerRef}\u0000${item.deviceId}`}>
							{item.providerName} / {item.deviceName}{item.deviceType ? `（${item.deviceType}）` : ''}{item.online === false ? ' · 离线' : ''}
						</option>)}
					</select>
					<small>视频仍由当前 Camera 驱动获取；隐私模式、状态灯、移动检测和云台等统一属性/命令转发给选中的 Xiaomi 中枢或 MIoT Provider。这里只保存 Provider 与设备引用，不保存或展示 Token。</small>
					{controlOptions.length === 0 && <small className="field-error">还没有可用的控制来源。请先返回 Provider，在 Xiaomi 中枢或 MIoT Cloud 的“管理设备”中扫描摄像头，保持统一模型为 camera，加入映射并保存；随后回到这里即可选择。</small>}
					{selectedControlIsMissing && <small className="field-error">原控制设备当前未出现在目录中，绑定会保留以便 Provider 恢复后继续使用；也可以在此更换或取消。</small>}
				</label>
				{driver === 'rtsp' ? <>
					<label>RTSP Host<input aria-label="RTSP Host" value={String(rtsp.host ?? '')} onChange={(event) => updateNested('rtsp', 'host', event.target.value)} placeholder="192.168.1.20" /></label>
					<label>端口<input aria-label="RTSP 端口" type="number" min="1" max="65535" value={Number(rtsp.port ?? 554)} onChange={(event) => updateNested('rtsp', 'port', Number(event.target.value))} /></label>
					<label className="wide">Path<input aria-label="RTSP Path" value={String(rtsp.path ?? '')} onChange={(event) => updateNested('rtsp', 'path', event.target.value)} placeholder="/live/main" /></label>
					<label>用户名<input aria-label="RTSP 用户名" value={String(rtsp.username ?? '')} onChange={(event) => updateNested('rtsp', 'username', event.target.value)} /></label>
					<label>密码<input aria-label="RTSP 密码" type="password" value={String(rtsp.password ?? '')} onChange={(event) => updateNested('rtsp', 'password', event.target.value)} /></label>
				</> : driver === 'onvif' ? <>
					<label>ONVIF Host<input aria-label="ONVIF Host" value={String(onvif.host ?? '')} onChange={(event) => updateNested('onvif', 'host', event.target.value)} placeholder="192.168.1.20" /></label>
					<label>端口<input aria-label="ONVIF 端口" type="number" min="1" max="65535" value={Number(onvif.port ?? 80)} onChange={(event) => updateNested('onvif', 'port', Number(event.target.value))} /></label>
					<label className="wide">Profile<input aria-label="ONVIF Profile" value={String(onvif.profile ?? '')} onChange={(event) => updateNested('onvif', 'profile', event.target.value)} placeholder="留空时使用设备默认码流" /></label>
					<label>用户名<input aria-label="ONVIF 用户名" value={String(onvif.username ?? '')} onChange={(event) => updateNested('onvif', 'username', event.target.value)} /></label>
					<label>密码<input aria-label="ONVIF 密码" type="password" value={String(onvif.password ?? '')} onChange={(event) => updateNested('onvif', 'password', event.target.value)} /></label>
				</> : <>
					<label>DID<input aria-label="小米摄像头 DID" value={String(xiaomi.did ?? '')} onChange={(event) => updateNested('xiaomi', 'did', event.target.value)} /></label>
					<label>型号<input aria-label="小米摄像头型号" value={String(xiaomi.model ?? '')} onChange={(event) => updateNested('xiaomi', 'model', event.target.value)} placeholder="chuangmi.camera.079ac1" /></label>
					<label>局域网 IP<input aria-label="小米摄像头局域网 IP" value={String(xiaomi.localIp ?? '')} onChange={(event) => updateNested('xiaomi', 'localIp', event.target.value)} /></label>
					<label>地区<input aria-label="小米摄像头地区" value={String(xiaomi.region ?? 'cn')} onChange={(event) => updateNested('xiaomi', 'region', event.target.value)} /></label>
					<label>视频子类型（subtype）<select aria-label="小米摄像头视频子类型" value={String(xiaomi.subtype ?? 'hd')} onChange={(event) => updateNested('xiaomi', 'subtype', event.target.value)}>{xiaomiSubtypes.map(([value, label]) => <option key={value} value={value}>{label}（{value}）</option>)}</select><small>用于 Xiaomi MISS 选择媒体档位；默认使用高清。设备不支持某个厂商码流时请改用自动、标清或高清。</small></label>
					<label className="wide">账号认证<select aria-label="小米摄像头账号认证" value={String(xiaomi.credentialProviderRef ?? '')} onChange={(event) => updateNested('xiaomi', 'credentialProviderRef', event.target.value)}><option value="">独立凭据</option>{credentialProviders.map((item) => <option key={item.id} value={item.id}>复用 {item.name}（{item.id}）</option>)}</select><small>复用时只保存 Provider 引用；密码和 Token 仅在后端授权边界读取，不经过浏览器或 Camera 配置。</small></label>
					{!String(xiaomi.credentialProviderRef ?? '') && <>
						<label>小米账号<input aria-label="小米摄像头账号" value={String(xiaomi.username ?? '')} onChange={(event) => updateNested('xiaomi', 'username', event.target.value)} /></label>
						<label>账号密码<input aria-label="小米摄像头密码" type="password" value={String(xiaomi.password ?? '')} onChange={(event) => updateNested('xiaomi', 'password', event.target.value)} /></label>
						<label>User ID<input aria-label="小米摄像头 User ID" value={String(xiaomi.userId ?? '')} onChange={(event) => updateNested('xiaomi', 'userId', event.target.value)} /></label>
						<label>Pass Token<input aria-label="小米摄像头 Pass Token" type="password" value={String(xiaomi.passToken ?? '')} onChange={(event) => updateNested('xiaomi', 'passToken', event.target.value)} /></label>
					</>}
				</>}
			</div>
			<div className="form-actions"><button type="button" onClick={() => setShowEditor(false)}>取消</button><button type="button" className="primary" onClick={applyDraft}>{editingID ? '更新子设备' : '加入子设备'}</button></div>
		</div>}
		<div className="provider-device-list"><div className="command-heading"><h3>已配置摄像头</h3><span>{entries.length} 台</span></div>{entries.length === 0 ? <p>尚未添加摄像头。先保持 Provider 运行，再点击“添加摄像头”。</p> : entries.map((entry) => {
			const binding = objectValue(entry.control)
			const controlSource = controlOptions.find((item) => item.providerRef === binding.providerRef && item.deviceId === binding.deviceId)
			return <div key={String(entry.id)}><span className={`status-dot ${entry.enabled === false ? '' : 'is-online'}`} /><strong>{String(entry.name || entry.id)}</strong><code>{String(entry.id)}</code><small>{String(entry.driver)} · {entry.connectionMode === 'always_on' ? '长连接' : entry.connectionMode === 'preload' ? '自动预连接' : '按需连接'} · {entry.enabled === false ? '已停用' : '已启用'}</small><small>{binding.providerRef && binding.deviceId ? `控制：${controlSource ? `${controlSource.providerName} / ${controlSource.deviceName}` : `${String(binding.providerRef)} / ${String(binding.deviceId)}（暂不可用）`}` : '控制：未绑定（仅视频）'}</small><button type="button" onClick={() => beginEdit(entry)}>编辑</button><button type="button" className="is-danger" onClick={() => replaceEntries(entries.filter((item) => String(item.id) !== String(entry.id)))}>移除</button></div>
		})}</div>
		<details><summary>摄像头子设备高级 JSON</summary><textarea aria-label="摄像头子设备 JSON" rows={14} value={entriesJSON} onChange={(event) => setEntriesJSON(event.target.value)} spellCheck={false} /><small>用于多码流及 RTSP、ONVIF、Xiaomi MISS 的高级字段。保存时以后端严格校验结果为准。</small></details>
		<div className="form-actions"><button type="button" onClick={onClose}>取消</button><button type="button" className="primary" disabled={!connected || saving} onClick={() => void save()}>{saving ? '应用中…' : '保存子设备并应用'}</button></div>
	</section>
}
