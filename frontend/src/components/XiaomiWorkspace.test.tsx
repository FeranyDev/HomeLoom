import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { XiaomiWorkspace } from './XiaomiWorkspace'

const api = vi.hoisted(() => ({ discoverXiaomiGateways: vi.fn() }))
vi.mock('../api/xiaomi', () => ({ discoverXiaomiGateways: api.discoverXiaomiGateways }))

const provider: Provider = {
	id: 'xiaomi-main', type: 'xiaomi', name: '家庭中枢', enabled: true, status: 'running', retryCount: 0,
	capabilities: { discovery: true, propertyRead: true, propertyWrite: true, commands: true, events: true },
	metrics: { requests: 18, events: 6, errors: 1 },
	config: { host: '192.168.1.50', port: 8883, clientId: '123', clientCertificate: 'certificate', privateKey: '********', pollIntervalSeconds: 60, oauth: { clientId: '1', oauthUuid: 'uuid', virtualDid: '123', region: 'cn', uid: '456' }, devices: [{ did: 'device-1' }] },
}

describe('XiaomiWorkspace', () => {
	beforeEach(() => vi.clearAllMocks())

	it('shows onboarding when no Xiaomi provider exists', async () => {
		const onCreate = vi.fn()
		render(<XiaomiWorkspace providers={[]} devices={[]} onCreate={onCreate} onEdit={() => {}} onManageDevices={() => {}} onDelete={() => {}} onRestart={vi.fn()} onTest={vi.fn()} />)
		expect(screen.getByText('还没有接入米家')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '＋ 接入米家' }))
		expect(onCreate).toHaveBeenCalledOnce()
	})

	it('shows authorization, hub, runtime metrics and discovered gateways', async () => {
		api.discoverXiaomiGateways.mockResolvedValue([{ instance: 'central-hub', hostName: 'hub.local', addresses: ['192.168.1.50'], port: 8883, did: 'hub-did', role: 1, mqttEnabled: true }])
		const onManageDevices = vi.fn()
		render(<XiaomiWorkspace providers={[provider]} devices={[]} onCreate={() => {}} onEdit={() => {}} onManageDevices={onManageDevices} onDelete={() => {}} onRestart={vi.fn()} onTest={vi.fn()} />)
		expect(screen.getByRole('heading', { name: '家庭中枢' })).toBeInTheDocument()
		expect(screen.getByText('已建立')).toBeInTheDocument()
		expect(screen.getByText('请求')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '发现中枢网关' }))
		expect(await screen.findByText('central-hub')).toBeInTheDocument()
		expect(screen.getByText('本地 MQTT 可用')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '管理子设备' }))
		expect(onManageDevices).toHaveBeenCalledWith(provider)
	})
})
