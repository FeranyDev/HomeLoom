import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { ProviderWorkspace } from './ProviderWorkspace'

const base = { enabled: true, status: 'running', retryCount: 0, capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: true } }
const providers: Provider[] = [
	{ ...base, id: 'virtual-main', type: 'virtual', name: 'Virtual', config: { devices: [{ id: 'demo' }] } },
	{ ...base, id: 'mqtt-main', type: 'mqtt', name: 'MQTT', config: { brokerUrl: 'mqtt://localhost:1883', devices: [{ id: 'lamp', topicPrefix: 'home/lamp', qos: 1 }] }, metrics: { messagesReceived: 12 } },
	{ ...base, id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', config: { host: '192.168.1.50', port: 8883, clientId: '123', clientCertificate: 'certificate', privateKey: 'key', oauth: { clientId: '1', oauthUuid: 'uuid', virtualDid: '123' }, devices: [] } },
	{ ...base, id: 'xiaomi-miot-cloud-main', type: 'xiaomi-miot-cloud', name: 'MIoT 云', capabilities: { ...base.capabilities, events: false }, config: { region: 'cn', username: 'owner@example.com', password: '********', devices: [] } },
	{ ...base, id: 'tuya-main', type: 'tuya', name: '涂鸦云', config: { region: 'cn', accessId: 'access-id', accessSecret: '********', uid: 'uid-123' } },
]

describe('ProviderWorkspace', () => {
	it('uses one lifecycle and full provider list for every type', () => {
		render(<ProviderWorkspace providers={providers} devices={[]} onEdit={() => {}} onManageDevices={() => {}} onDelete={() => {}} onRestart={vi.fn()} onTest={vi.fn()} onSimulate={vi.fn()} />)
		expect(screen.getByLabelText('Provider 运行流程')).toBeInTheDocument()
		expect(screen.getAllByText('运行连接')).toHaveLength(5)
		expect(screen.getByText('Virtual Runtime · virtual-main')).toBeInTheDocument()
		expect(screen.getByText('MQTT 客户端（CLIENT） · HomeLoom v1 · mqtt-main')).toBeInTheDocument()
		expect(screen.getByText('小米中枢网关 · xiaomi-main')).toBeInTheDocument()
		expect(screen.getByText('小米 MIoT 云 · 第三方兼容 · xiaomi-miot-cloud-main')).toBeInTheDocument()
		expect(screen.getByText('Tuya 涂鸦云 · tuya-main')).toBeInTheDocument()
	})

	it('keeps gateway discovery out of the provider overview and opens Xiaomi device management', async () => {
		const onManageDevices = vi.fn()
		render(<ProviderWorkspace providers={[providers[2]]} devices={[]} onEdit={() => {}} onManageDevices={onManageDevices} onDelete={() => {}} onRestart={vi.fn()} onTest={vi.fn()} onSimulate={vi.fn()} />)
		expect(screen.queryByRole('button', { name: '发现小米中枢网关' })).not.toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '管理设备' }))
		expect(onManageDevices).toHaveBeenCalledWith(providers[2])
	})

	it('opens device management for an MQTT Broker connection', async () => {
		const onManageDevices = vi.fn()
		render(<ProviderWorkspace providers={[providers[1]]} devices={[]} onEdit={() => {}} onManageDevices={onManageDevices} onDelete={() => {}} onRestart={vi.fn()} onTest={vi.fn()} onSimulate={vi.fn()} />)
		await userEvent.click(screen.getByRole('button', { name: '管理设备' }))
		expect(onManageDevices).toHaveBeenCalledWith(providers[1])
		expect(screen.getByText('数据库设备路由 · 严格白名单')).toBeInTheDocument()
	})
})
