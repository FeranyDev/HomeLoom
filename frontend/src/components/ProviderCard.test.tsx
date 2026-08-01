import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ProviderCard } from './ProviderCard'
import type { Provider } from '../types/provider'

const provider: Provider = { id: 'virtual-main', type: 'virtual', name: 'Virtual', enabled: true, config: {}, status: 'running', retryCount: 0, capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: true } }

describe('ProviderCard simulation', () => {
	it('distinguishes MQTT client and server runtime modes', () => {
		const server: Provider = { ...provider, id: 'mqtt-server', type: 'mqtt', name: '设备接入', config: { mode: 'server', listenAddress: '0.0.0.0:1883', devices: [] } }
		render(<ProviderCard provider={server} devices={[]} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={vi.fn()} onTest={vi.fn()} />)
		expect(screen.getByText(/MQTT 服务端（SERVER）/)).toBeInTheDocument()
		expect(screen.getByText('mqtt-server')).toHaveClass('provider')
		expect(screen.getByText('服务端监听中')).toBeInTheDocument()
		expect(screen.getByText('0.0.0.0:1883')).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '监听配置' })).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '重启监听' })).toBeInTheDocument()
	})

	it('shows Camera Provider media lifecycle and child-device action', () => {
		const camera: Provider = { ...provider, id: 'camera-main', type: 'camera', name: '家庭摄像头', config: { cameras: [{ id: 'front-door', name: '门口', driver: 'rtsp' }] } }
		render(<ProviderCard provider={camera} devices={[]} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={vi.fn()} onManageDevices={vi.fn()} />)
		expect(screen.getByText('1 台子设备')).toBeInTheDocument()
		expect(screen.getByText('Media Worker / Camera Kernel 已按 Provider 启用')).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '管理摄像头' })).toBeInTheDocument()
	})

	it('shows renewable credential deadlines without exposing credential values', () => {
		const xiaomi: Provider = { ...provider, id: 'xiaomi-main', type: 'xiaomi', config: { oauth: { clientId: '1', oauthUuid: 'uuid', virtualDid: '2' }, clientId: '2', clientCertificate: '********', privateKey: '********' }, credentials: { managed: true, refreshAt: '2026-07-16T10:00:00Z', tokenExpiresAt: '2026-07-16T12:00:00Z', certificateExpiresAt: '2026-08-16T12:00:00Z' }, credentialError: 'cloud unavailable', credentialRetryAt: '2026-07-16T10:01:00Z' }
		render(<ProviderCard provider={xiaomi} devices={[]} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={vi.fn()} />)
		expect(screen.getByText('凭据自动续期')).toBeInTheDocument()
		expect(screen.getByRole('alert')).toHaveTextContent('续期失败：cloud unavailable')
		expect(screen.queryByText('********')).not.toBeInTheDocument()
	})

	it('distinguishes the third-party MIoT cloud from central hub and future official cloud', () => {
		const cloud: Provider = { ...provider, id: 'xiaomi-miot-cloud-main', type: 'xiaomi-miot-cloud', name: 'MIoT 云', config: { region: 'cn', username: 'owner@example.com', password: '********', pollIntervalSeconds: 30, devices: [] }, capabilities: { ...provider.capabilities, events: false } }
		render(<ProviderCard provider={cloud} devices={[]} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={vi.fn()} onManageDevices={vi.fn()} />)
		expect(screen.getByText('小米 MIoT 云 · 第三方兼容 · xiaomi-miot-cloud-main')).toBeInTheDocument()
		expect(screen.getByText('轮询 30 秒 · 非官方兼容接口')).toBeInTheDocument()
		expect(screen.queryByText('小米中枢网关')).not.toBeInTheDocument()
	})

	it('shows the actual runtime mode for each published MIoT cloud device', () => {
		const cloud: Provider = { ...provider, id: 'xiaomi-miot-cloud-main', type: 'xiaomi-miot-cloud', name: 'MIoT 云', config: { username: 'owner@example.com', password: '********', devices: [] } }
		const devices = [
			{ schemaVersion: 1, id: 'local-air-conditioner', providerId: cloud.id, name: '客厅空调', type: 'air-conditioner' as const, availability: 'online' as const, online: true, runtimeMode: 'local' as const, endpoints: [], lastUpdateAt: '' },
			{ schemaVersion: 1, id: 'cloud-air-conditioner', providerId: cloud.id, name: '卧室空调', type: 'air-conditioner' as const, availability: 'online' as const, online: true, runtimeMode: 'cloud' as const, endpoints: [], lastUpdateAt: '' },
		]
		render(<ProviderCard provider={cloud} devices={devices} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={vi.fn()} />)
		expect(screen.getByText('局域网')).toHaveClass('device-runtime-mode', 'is-local')
		expect(screen.getByText('云端轮询')).toHaveClass('device-runtime-mode', 'is-cloud')
	})

	it('shows central gateway local/cloud route metrics and device runtime mode', () => {
		const central: Provider = { ...provider, id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', config: { host: '192.168.1.50', port: 8883, oauth: { clientId: '1', oauthUuid: 'uuid', virtualDid: '2' }, clientId: '2', clientCertificate: '********', privateKey: '********', devices: [] }, metrics: { requests: 18, errors: 0, localRequests: 12, localFailures: 2, cloudFallbacks: 2, cloudRequests: 6, cloudMqttConfigured: 1, cloudMqttConnected: 1, cloudMqttMessagesReceived: 9 } }
		const devices = [{ schemaVersion: 1, id: 'cloud-ac', providerId: central.id, name: 'Wi-Fi 空调', type: 'air-conditioner' as const, availability: 'online' as const, online: true, runtimeMode: 'cloud' as const, stateTransport: 'cloud-mqtt' as const, endpoints: [], lastUpdateAt: '' }]
		const { container } = render(<ProviderCard provider={central} devices={devices} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={vi.fn()} />)
		expect(container.querySelector('.provider-status-grid')?.children).toHaveLength(5)
		expect(screen.getByText(/MQTT 本地优先 \/ OAuth 官方云回退/)).toBeInTheDocument()
		expect(screen.getByText(/^累计请求/)).toHaveTextContent('18')
		expect(screen.getByText(/^最终失败/)).toHaveTextContent('0')
		expect(screen.getByText(/^本地请求/)).toHaveTextContent('12')
		expect(screen.getByText(/^本地失败/)).toHaveTextContent('2')
		expect(screen.getByText(/^自动转云/)).toHaveTextContent('2')
		expect(screen.getByText(/^云 HTTP 请求/)).toHaveTextContent('6')
		expect(screen.getAllByText(/^官方云 MQTT/).some((item) => item.textContent?.includes('已连接'))).toBe(true)
		expect(screen.getByText(/^云 MQTT 推送/)).toHaveTextContent('9')
		expect(screen.getByText('官方云实时')).toHaveClass('device-runtime-mode', 'is-cloud')
	})

	it('shows sanitized official cloud MQTT reconnect diagnostics', () => {
		const central: Provider = { ...provider, id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', config: { oauth: { clientId: '1', oauthUuid: 'uuid', virtualDid: '2' }, devices: [] }, metrics: { cloudMqttConfigured: 1 }, diagnostics: { cloudMqttState: 'reconnecting', cloudMqttLastError: 'connection refused', cloudMqttNextRetryAt: '2026-07-22T10:00:00Z' } }
		render(<ProviderCard provider={central} devices={[]} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={vi.fn()} />)
		expect(screen.getAllByText(/^官方云 MQTT/).some((item) => item.textContent?.includes('重连中'))).toBe(true)
		expect(screen.getByText(/connection refused/)).toHaveTextContent('重试')
	})

  it('sends ephemeral availability and temperature changes', async () => {
    const onSimulate = vi.fn().mockResolvedValue(undefined); const onRestart = vi.fn().mockResolvedValue(undefined)
    render(<ProviderCard provider={provider} devices={[{ schemaVersion: 1, id: 'temp-1', providerId: provider.id, name: '温度', type: 'temperature-sensor', availability: 'online', online: true, endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [{ id: 'temperature', type: 'temperature', properties: [{ definition: { id: 'current-temperature', name: '当前温度', type: 'number', unit: 'celsius', readable: true, writable: false, notifiable: true }, value: { type: 'number', number: 20 } }] }] }], lastUpdateAt: '' }]} onEdit={() => {}} onDelete={() => {}} onRestart={onRestart} onSimulate={onSimulate} />)
    await userEvent.click(screen.getByRole('button', { name: '设为离线' })); expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'temp-1' }), { online: false })
    const input = screen.getByLabelText('温度温度'); await userEvent.clear(input); await userEvent.type(input, '19.5'); await userEvent.click(screen.getByRole('button', { name: '上报' })); expect(onSimulate).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'temp-1' }), { temperature: 19.5 })
		await userEvent.click(screen.getByRole('button', { name: '重复事件' })); expect(onSimulate).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'temp-1' }), { repeat: 2 })
		await userEvent.click(screen.getByRole('button', { name: '旧序列事件' })); expect(onSimulate).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'temp-1' }), { sequence: 1 })
    await userEvent.click(screen.getByRole('button', { name: '重新连接' })); expect(onRestart).toHaveBeenCalledWith(provider)
  })

  it('sends humidity, contact, and motion changes', async () => {
    const onSimulate = vi.fn().mockResolvedValue(undefined)
    const devices = [
      { schemaVersion: 1, id: 'humidity', providerId: provider.id, name: '湿度', type: 'humidity-sensor' as const, availability: 'online' as const, online: true, endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [{ id: 'humidity', type: 'humidity', properties: [{ definition: { id: 'current-humidity', name: '当前湿度', type: 'number' as const, unit: 'percent', readable: true, writable: false, notifiable: true }, value: { type: 'number' as const, number: 50 } }] }] }], lastUpdateAt: '' },
      { schemaVersion: 1, id: 'door', providerId: provider.id, name: '门', type: 'contact-sensor' as const, availability: 'online' as const, online: true, endpoints: [], lastUpdateAt: '' },
      { schemaVersion: 1, id: 'motion', providerId: provider.id, name: '活动', type: 'motion-sensor' as const, availability: 'online' as const, online: true, endpoints: [], lastUpdateAt: '' },
    ]
    render(<ProviderCard provider={provider} devices={devices} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={onSimulate} />)
    const humidity = screen.getByLabelText('湿度湿度'); await userEvent.clear(humidity); await userEvent.type(humidity, '63.5'); await userEvent.click(screen.getByRole('button', { name: '上报' }))
    await userEvent.click(screen.getByRole('button', { name: '设为闭合' })); await userEvent.click(screen.getByRole('button', { name: '触发活动' }))
    expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'humidity' }), { humidity: 63.5 })
    expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'door' }), { contact: true })
    expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'motion' }), { motion: true })
  })

	it('simulates air purifier and filter state', async () => {
		const onSimulate = vi.fn().mockResolvedValue(undefined)
		const purifier = { schemaVersion: 1, id: 'air', providerId: provider.id, name: '净化器', type: 'air-purifier' as const, availability: 'online' as const, online: true, lastUpdateAt: '', endpoints: [{ id: 'main', name: 'Main', type: 'air-purifier', capabilities: [{ id: 'air-purifier', type: 'air-purifier', properties: [{ definition: { id: 'active', name: '启用', type: 'bool' as const, readable: true, writable: true, notifiable: true }, value: { type: 'bool' as const, bool: true } }, { definition: { id: 'target-state', name: '模式', type: 'enum' as const, readable: true, writable: true, notifiable: true, enum: ['manual', 'auto'] }, value: { type: 'enum' as const, string: 'auto' } }, { definition: { id: 'rotation-speed', name: '速度', type: 'number' as const, readable: true, writable: true, notifiable: true }, value: { type: 'number' as const, number: 60 } }] }, { id: 'filter', type: 'filter-maintenance', properties: [{ definition: { id: 'life-level', name: '寿命', type: 'number' as const, readable: true, writable: false, notifiable: true }, value: { type: 'number' as const, number: 8 } }, { definition: { id: 'change-indication', name: '更换', type: 'bool' as const, readable: true, writable: false, notifiable: true }, value: { type: 'bool' as const, bool: true } }] }] }] }
		render(<ProviderCard provider={provider} devices={[purifier]} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={onSimulate} />)
		await userEvent.click(screen.getByRole('button', { name: '停止' })); await userEvent.click(screen.getByRole('button', { name: '手动模式' })); await userEvent.click(screen.getByRole('button', { name: '标记滤芯正常' }))
		expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'air' }), { active: false })
		expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'air' }), { mode: 'manual' })
		expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'air' }), { filterChange: false })
	})
})
