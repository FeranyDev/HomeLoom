import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ProviderCard } from './ProviderCard'
import type { Provider } from '../types/provider'

const provider: Provider = { id: 'virtual-main', type: 'virtual', name: 'Virtual', enabled: true, config: {}, status: 'running', retryCount: 0, capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: true } }

describe('ProviderCard simulation', () => {
  it('sends ephemeral availability and temperature changes', async () => {
    const onSimulate = vi.fn().mockResolvedValue(undefined); const onRestart = vi.fn().mockResolvedValue(undefined)
    render(<ProviderCard provider={provider} devices={[{ schemaVersion: 1, id: 'temp-1', providerId: provider.id, name: '温度', type: 'single-property-sensor', availability: 'online', online: true, endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [{ id: 'sensor', type: 'sensor', properties: [{ definition: { id: 'value', name: '传感器值', type: 'number', unit: 'celsius', readable: true, writable: false, notifiable: true }, value: { type: 'number', number: 20 } }] }] }], lastUpdateAt: '' }]} onEdit={() => {}} onDelete={() => {}} onRestart={onRestart} onSimulate={onSimulate} />)
    await userEvent.click(screen.getByRole('button', { name: '设为离线' })); expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'temp-1' }), { online: false })
    const input = screen.getByLabelText('温度传感器值'); await userEvent.clear(input); await userEvent.type(input, '19.5'); await userEvent.click(screen.getByRole('button', { name: '上报' })); expect(onSimulate).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'temp-1' }), { value: 19.5 })
		await userEvent.click(screen.getByRole('button', { name: '重复事件' })); expect(onSimulate).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'temp-1' }), { repeat: 2 })
		await userEvent.click(screen.getByRole('button', { name: '旧序列事件' })); expect(onSimulate).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'temp-1' }), { sequence: 1 })
    await userEvent.click(screen.getByRole('button', { name: '重新连接' })); expect(onRestart).toHaveBeenCalledWith(provider)
  })

  it('sends humidity, contact, and motion changes', async () => {
    const onSimulate = vi.fn().mockResolvedValue(undefined)
    const devices = [
      { schemaVersion: 1, id: 'humidity', providerId: provider.id, name: '湿度', type: 'single-property-sensor' as const, availability: 'online' as const, online: true, endpoints: [{ id: 'main', name: 'Main', type: 'sensor', capabilities: [{ id: 'sensor', type: 'sensor', properties: [{ definition: { id: 'value', name: '传感器值', type: 'number' as const, unit: 'percent', readable: true, writable: false, notifiable: true }, value: { type: 'number' as const, number: 50 } }] }] }], lastUpdateAt: '' },
      { schemaVersion: 1, id: 'door', providerId: provider.id, name: '门', type: 'contact-sensor' as const, availability: 'online' as const, online: true, endpoints: [], lastUpdateAt: '' },
      { schemaVersion: 1, id: 'motion', providerId: provider.id, name: '活动', type: 'motion-sensor' as const, availability: 'online' as const, online: true, endpoints: [], lastUpdateAt: '' },
    ]
    render(<ProviderCard provider={provider} devices={devices} onEdit={() => {}} onDelete={() => {}} onRestart={vi.fn()} onSimulate={onSimulate} />)
    const humidity = screen.getByLabelText('湿度传感器值'); await userEvent.clear(humidity); await userEvent.type(humidity, '63.5'); await userEvent.click(screen.getByRole('button', { name: '上报' }))
    await userEvent.click(screen.getByRole('button', { name: '设为闭合' })); await userEvent.click(screen.getByRole('button', { name: '触发活动' }))
    expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'humidity' }), { value: 63.5 })
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
