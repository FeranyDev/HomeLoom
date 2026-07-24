import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { TargetCard } from './TargetCard'
import type { MatterTarget, Target } from '../types/target'

const target = {
  id: 'apple-main', type: 'apple-hap' as const, name: '主桥', enabled: true, status: 'running' as const,
  config: { address: ':51826', setupId: 'HLM1' }, pairing: { pairingCode: '001-02-003', setupUri: 'X-HM://test', paired: false }, deviceIds: [], devices: [],
}

const callbacks = () => ({ onEdit: vi.fn(), onManageDevices: vi.fn(), onDelete: vi.fn(), onRegeneratePairing: vi.fn(), onClearPairingIdentity: vi.fn() })

describe('TargetCard', () => {
  it('loads the pairing QR only after explicit user action', async () => {
    render(<TargetCard target={target} {...callbacks()} />)
		expect(screen.getByRole('button', { name: '重新生成 HomeKit 配对参数' })).toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '清除 HomeKit 配对身份' })).not.toBeInTheDocument()
    expect(screen.queryByRole('img', { name: /HomeKit 配对二维码/ })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '显示配对二维码' }))
    expect(screen.getByRole('img', { name: /HomeKit 配对二维码/ })).toHaveAttribute('src', '/api/v1/targets/apple-main/pairing-qr')
    await userEvent.click(screen.getByRole('button', { name: '隐藏二维码' }))
    expect(screen.queryByRole('img', { name: /HomeKit 配对二维码/ })).not.toBeInTheDocument()
  })

	it('collapses one-time setup details after pairing', () => {
		render(<TargetCard target={{ ...target, pairing: { paired: true } }} {...callbacks()} />)
		expect(screen.getByText('已配对至 Apple Home')).toBeInTheDocument()
		expect(screen.getByText('已连接 Apple Home')).toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '显示配对二维码' })).not.toBeInTheDocument()
		expect(screen.queryByRole('button', { name: '重新生成 HomeKit 配对参数' })).not.toBeInTheDocument()
		expect(screen.getByRole('button', { name: '清除 HomeKit 配对身份' })).toBeInTheDocument()
	})

  it('renders Matter runtime fields and only renders the QR while its window is open', () => {
    const matter: MatterTarget = {
      id: 'matter-main', type: 'matter', name: 'Matter 目标', enabled: true, status: 'running', deviceIds: [], devices: [],
      config: { networkInterface: 'en0', udpPort: 5540, protocolVersion: '1.3' }, commissioning: { state: 'commissioned', windowOpen: false },
      fabricCount: 2, endpointCount: 5, fabrics: [{ id: 'fabric-1', label: 'Apple Home' }], runtime: { interface: 'en0', protocolVersion: '1.3' }, certification: 'test',
    }
    const onToggle = vi.fn()
    render(<TargetCard target={matter} {...callbacks()} onMatterCommissioningToggle={onToggle} onDeleteMatterFabric={vi.fn()} onFactoryResetMatter={vi.fn()} />)
    expect(screen.getByText('2 个 Fabric · 5 个 Endpoint')).toBeInTheDocument()
    expect(screen.getAllByText('测试设备 · 未认证')).toHaveLength(2)
    expect(screen.queryByRole('img', { name: /Matter 配网二维码/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '打开配网窗口' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '删除 Fabric Apple Home' })).toBeInTheDocument()
    expect(screen.queryByText('HAP 监听地址')).not.toBeInTheDocument()
  })

	it('shows Matter commissioning QR and close action only for an open window', () => {
		const matter: MatterTarget = {
			id: 'matter-main', type: 'matter', name: 'Matter 目标', enabled: true, status: 'running', deviceIds: [], devices: [], config: {},
			commissioning: { state: 'window-open', windowOpen: true, manualPairingCode: '349701123' }, fabricCount: 0, endpointCount: 2, certification: 'unknown',
		}
		render(<TargetCard target={matter} {...callbacks()} onMatterCommissioningToggle={vi.fn()} />)
		expect(screen.getByRole('img', { name: /Matter 配网二维码/ })).toHaveAttribute('src', '/api/v1/targets/matter-main/commissioning-qr')
		expect(screen.getByText('手工配对码：349701123')).toBeInTheDocument()
		expect(screen.getByRole('button', { name: '关闭配网窗口' })).toBeInTheDocument()
	})

	it('shows detailed bridge projection issues for HomeKit targets', () => {
		const broken: Target = {
			...target,
			error: '1 台设备未能发布到桥：坏掉的开关: consumer "homekit" requires parameter main/switch/power',
			diagnostics: { skippedAccessories: '1', publishedAccessories: '1' },
			issues: [{ deviceId: 'broken-switch', deviceName: '坏掉的开关', deviceType: 'switch', stage: 'consumer-contract', message: 'consumer "homekit" requires parameter main/switch/power' }],
		}
		render(<TargetCard target={broken} {...callbacks()} />)
		expect(screen.getAllByText(/1 台设备未能发布到桥/).length).toBeGreaterThan(0)
		expect(screen.getByText('坏掉的开关')).toBeInTheDocument()
		expect(screen.getByText('consumer-contract')).toBeInTheDocument()
		expect(screen.getByText('consumer "homekit" requires parameter main/switch/power')).toBeInTheDocument()
		expect(screen.getByText(/已跳过配件 1/)).toBeInTheDocument()
	})
})

