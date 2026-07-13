import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ProviderCard } from './ProviderCard'
import type { Provider } from '../types/provider'

const provider: Provider = { id: 'virtual-main', type: 'virtual', name: 'Virtual', enabled: true, config: {}, status: 'running', capabilities: { discovery: true, propertyRead: true, propertyWrite: true, events: true } }

describe('ProviderCard simulation', () => {
  it('sends ephemeral availability and temperature changes', async () => {
    const onSimulate = vi.fn().mockResolvedValue(undefined)
    render(<ProviderCard provider={provider} devices={[{ id: 'temp-1', providerId: provider.id, name: '温度', type: 'temperature-sensor', online: true, state: { temperature: 22 }, endpoints: [], lastUpdateAt: '' }]} onEdit={() => {}} onDelete={() => {}} onSimulate={onSimulate} />)
    await userEvent.click(screen.getByRole('button', { name: '设为离线' })); expect(onSimulate).toHaveBeenCalledWith(expect.objectContaining({ id: 'temp-1' }), { online: false })
    const input = screen.getByLabelText('温度温度'); await userEvent.clear(input); await userEvent.type(input, '19.5'); await userEvent.click(screen.getByRole('button', { name: '上报' })); expect(onSimulate).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'temp-1' }), { temperature: 19.5 })
  })
})
