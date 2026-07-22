import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { TargetCard } from './TargetCard'

const target = {
  id: 'apple-main', type: 'apple-hap' as const, name: '主桥', enabled: true,
  status: 'running' as const, address: ':51826', pairingCode: '001-02-003',
  setupUri: 'X-HM://test', setupId: 'HLM1', paired: false, deviceIds: [],
	devices: [],
}

describe('TargetCard', () => {
  it('loads the pairing QR only after explicit user action', async () => {
    render(<TargetCard target={target} onEdit={vi.fn()} onManageDevices={vi.fn()} onDelete={vi.fn()} onRegeneratePairing={vi.fn()} onClearPairingIdentity={vi.fn()} />)
		expect(screen.getByRole('button', { name: '重新生成 HomeKit 配对参数' })).toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '清除 HomeKit 配对身份' })).not.toBeInTheDocument()
    expect(screen.queryByRole('img', { name: /HomeKit 配对二维码/ })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '显示配对二维码' }))
    expect(screen.getByRole('img', { name: /HomeKit 配对二维码/ })).toHaveAttribute('src', '/api/v1/targets/apple-main/pairing-qr')
    await userEvent.click(screen.getByRole('button', { name: '隐藏二维码' }))
    expect(screen.queryByRole('img', { name: /HomeKit 配对二维码/ })).not.toBeInTheDocument()
  })

	it('collapses one-time setup details after pairing', () => {
		render(<TargetCard target={{ ...target, paired: true, pairingCode: undefined, setupId: undefined, setupUri: undefined }} onEdit={vi.fn()} onManageDevices={vi.fn()} onDelete={vi.fn()} onRegeneratePairing={vi.fn()} onClearPairingIdentity={vi.fn()} />)
		expect(screen.getByText('已配对至 Apple Home')).toBeInTheDocument()
		expect(screen.getByText('已连接 Apple Home')).toBeInTheDocument()
		expect(screen.queryByText('HomeKit 配对码')).not.toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '显示配对二维码' })).not.toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '重新生成 HomeKit 配对参数' })).not.toBeInTheDocument()
		expect(screen.getByRole('button', { name: '清除 HomeKit 配对身份' })).toBeInTheDocument()
	})

  it('does not expose HomeKit controls for another Consumer adapter', () => {
    render(<TargetCard target={{ ...target, id: 'matter-main', type: 'matter', name: 'Matter 目标', setupUri: undefined, pairingCode: undefined, address: undefined }} onEdit={vi.fn()} onManageDevices={vi.fn()} onDelete={vi.fn()} onRegeneratePairing={vi.fn()} onClearPairingIdentity={vi.fn()} />)
    expect(screen.getByText('Matter（matter）')).toBeInTheDocument()
    expect(screen.getByText(/不会回退到 HomeKit/)).toBeInTheDocument()
    expect(screen.queryByText('HomeKit 配对码')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /配对参数/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /配对二维码/ })).not.toBeInTheDocument()
  })
})
