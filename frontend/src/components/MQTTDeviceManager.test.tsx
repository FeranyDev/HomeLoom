import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Provider } from '../types/provider'
import { MQTTDeviceManager } from './MQTTDeviceManager'

const provider: Provider = { id: 'mqtt-main', type: 'mqtt', name: '家庭 MQTT', enabled: true, config: { brokerUrl: 'mqtt://broker.local:1883', devices: [] }, status: 'running', retryCount: 0, capabilities: { discovery: true, propertyRead: true, propertyWrite: true, commands: true, events: true } }

describe('MQTTDeviceManager', () => {
	it('starts with a connected Broker but no implicit device subscriptions', () => {
		render(<MQTTDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={vi.fn()} />)
		expect(screen.getByText('外部 Broker 已连接')).toBeInTheDocument()
		expect(screen.getByText(/不会订阅或发布任意设备/)).toBeInTheDocument()
	})

	it('shows server listener and default-deny ACL semantics', () => {
		const server: Provider = { ...provider, id: 'mqtt-server', config: { mode: 'server', listenAddress: '0.0.0.0:1883', devices: [] } }
		render(<MQTTDeviceManager provider={server} devices={[]} onClose={() => {}} onSave={vi.fn()} />)
		expect(screen.getByText('MQTT 服务端正在监听')).toBeInTheDocument()
		expect(screen.getByText(/0.0.0.0:1883/)).toBeInTheDocument()
		expect(screen.getByText(/ACL 拒绝所有设备 Topic/)).toBeInTheDocument()
	})

	it('saves topic prefix, QoS, and expanded topics on one device route', async () => {
		const onSave = vi.fn().mockResolvedValue(undefined)
		render(<MQTTDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={onSave} />)
		await userEvent.type(screen.getByLabelText('MQTT 设备 ID'), 'living-room-light')
		await userEvent.type(screen.getByLabelText('MQTT 设备名称'), '客厅灯')
		await userEvent.type(screen.getByLabelText('MQTT 设备 Topic Prefix'), 'house/living-room')
		await userEvent.selectOptions(screen.getByLabelText('MQTT 设备 QoS'), '2')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByText('等待 retained Discovery')).toBeInTheDocument()
		await userEvent.click(screen.getByRole('button', { name: '保存设备并应用' }))
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ id: 'mqtt-main', config: expect.objectContaining({ brokerUrl: 'mqtt://broker.local:1883', devices: [expect.objectContaining({ id: 'living-room-light', name: '客厅灯', topicPrefix: 'house/living-room', qos: 2, protocol: 'homeloom-v1', topics: { discovery: 'house/living-room/discovery/living-room-light', availability: 'house/living-room/availability/living-room-light', state: 'house/living-room/state/living-room-light/{endpointId}/{capabilityId}/{propertyId}', command: 'house/living-room/command/living-room-light/{endpointId}/{capabilityId}/{operationId}' } })] }) }), true)
	})

	it('validates state topic placeholders before staging a route', async () => {
		render(<MQTTDeviceManager provider={provider} devices={[]} onClose={() => {}} onSave={vi.fn()} />)
		await userEvent.type(screen.getByLabelText('MQTT 设备 ID'), 'sensor')
		await userEvent.type(screen.getByLabelText('MQTT 设备 Topic Prefix'), 'house')
		await userEvent.type(screen.getByLabelText('MQTT State Topic'), 'house/sensor/state')
		await userEvent.click(screen.getByRole('button', { name: '加入草稿' }))
		expect(screen.getByRole('alert')).toHaveTextContent('State Topic 必须包含')
	})
})
