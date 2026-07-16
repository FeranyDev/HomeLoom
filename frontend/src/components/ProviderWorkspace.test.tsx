import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { ProviderWorkspace } from './ProviderWorkspace'

const api = vi.hoisted(() => ({ discoverXiaomiGateways: vi.fn() }))
vi.mock('../api/xiaomi', () => ({ discoverXiaomiGateways: api.discoverXiaomiGateways }))

const base = { enabled: true, status: 'running', retryCount: 0, capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: true } }
const providers: Provider[] = [
	{ ...base, id: 'virtual-main', type: 'virtual', name: 'Virtual', config: { devices: [{ id: 'demo' }] } },
	{ ...base, id: 'mqtt-main', type: 'mqtt', name: 'MQTT', config: { brokerUrl: 'mqtt://localhost:1883', topicPrefix: 'home' }, metrics: { messagesReceived: 12 } },
	{ ...base, id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', config: { host: '192.168.1.50', port: 8883, clientId: '123', clientCertificate: 'certificate', privateKey: 'key', oauth: { clientId: '1', oauthUuid: 'uuid', virtualDid: '123' }, devices: [] } },
	{ ...base, id: 'xiaomi-miot-cloud-main', type: 'xiaomi-miot-cloud', name: 'MIoT 云', capabilities: { ...base.capabilities, events: false }, config: { region: 'cn', username: 'owner@example.com', password: '********', devices: [] } },
]

describe('ProviderWorkspace', () => {
	beforeEach(() => vi.clearAllMocks())

	it('uses one lifecycle and full provider list for every type', () => {
		render(<ProviderWorkspace providers={providers} devices={[]} onEdit={() => {}} onManageDevices={() => {}} onDelete={() => {}} onRestart={vi.fn()} onTest={vi.fn()} onSimulate={vi.fn()} />)
		expect(screen.getByLabelText('Provider 运行流程')).toBeInTheDocument()
		expect(screen.getAllByText('运行连接')).toHaveLength(4)
		expect(screen.getByText('Virtual Runtime · virtual-main')).toBeInTheDocument()
		expect(screen.getByText('MQTT Broker · mqtt-main')).toBeInTheDocument()
		expect(screen.getByText('小米中枢网关 · xiaomi-main')).toBeInTheDocument()
		expect(screen.getByText('小米 MIoT 云 · 第三方兼容 · xiaomi-miot-cloud-main')).toBeInTheDocument()
	})

	it('keeps Xiaomi gateway discovery and device management in Provider page', async () => {
		api.discoverXiaomiGateways.mockResolvedValue([{ instance: 'central-hub', hostName: 'hub.local', addresses: ['192.168.1.50'], port: 8883, did: 'hub-did', role: 1, mqttEnabled: true }])
		const onManageDevices = vi.fn()
		render(<ProviderWorkspace providers={[providers[2]]} devices={[]} onEdit={() => {}} onManageDevices={onManageDevices} onDelete={() => {}} onRestart={vi.fn()} onTest={vi.fn()} onSimulate={vi.fn()} />)
		await userEvent.click(screen.getByRole('button', { name: '发现小米中枢网关' }))
		expect(await screen.findByText('central-hub')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '管理设备' }))
		expect(onManageDevices).toHaveBeenCalledWith(providers[2])
	})
})
