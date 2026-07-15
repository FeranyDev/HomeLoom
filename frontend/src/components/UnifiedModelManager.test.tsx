import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as api from '../api/mapping'
import type { ModelContract } from '../types/mapping'
import { UnifiedModelManager } from './UnifiedModelManager'

vi.mock('../api/mapping', () => ({
  listModelContracts: vi.fn(), listCustomModelProperties: vi.fn(), createCustomModelProperty: vi.fn(),
  updateCustomModelProperty: vi.fn(), deleteCustomModelProperty: vi.fn(), createCustomModel: vi.fn(), deleteCustomModel: vi.fn(),
}))

const model: ModelContract = {
  deviceType: 'lightbulb', version: 1, builtIn: true,
  custom: {
    publisher: { level: 'custom', behavior: 'preserve-and-mark-custom' },
    consumer: { level: 'custom', behavior: 'explicit-path-mapping-only' },
  },
  parameters: [
    { path: { endpointId: 'main', capabilityId: 'switch', propertyId: 'power' }, name: '开关', level: 'required', type: 'bool', readable: true, writable: true, notifiable: true, publisher: { level: 'required', behavior: 'must-publish' }, consumer: { level: 'required', behavior: 'must-map' }, publisherNotes: '必须发布', consumerNotes: '必须映射' },
    { path: { endpointId: 'main', capabilityId: 'light', propertyId: 'brightness' }, name: '亮度', level: 'optional', type: 'number', unit: 'percent', min: 0, max: 100, step: 1, readable: true, writable: true, notifiable: true, publisher: { level: 'optional', behavior: 'publish-if-supported' }, consumer: { level: 'optional', behavior: 'map-if-supported' } },
  ],
}

describe('UnifiedModelManager', () => {
  beforeEach(() => {
    vi.mocked(api.listModelContracts).mockResolvedValue([model])
    vi.mocked(api.listCustomModelProperties).mockResolvedValue([])
  })

  it('presents one unified model as a three-level field configuration', async () => {
    render(<UnifiedModelManager />)
    expect(await screen.findByRole('heading', { name: '模型与属性字段配置' })).toBeInTheDocument()
    expect(screen.getByText('灯泡（lightbulb）', { selector: 'h3' })).toBeInTheDocument()
    expect(screen.getByText('主端点（main）', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText('灯光（light）', { selector: 'strong' })).toBeInTheDocument()
    const brightness = screen.getByText(/亮度.*brightness/, { selector: 'strong' }).closest('article')!
    expect(within(brightness).getByText('数值（number）')).toBeInTheDocument()
    expect(within(brightness).getByText(/最小值（min）0.*最大值（max）100.*步长（step）1/)).toBeInTheDocument()
    expect(within(brightness).getByText(/支持时发布（publish-if-supported）/)).toBeInTheDocument()
  })

  it('filters the visible property level without changing the contract', async () => {
    render(<UnifiedModelManager />)
    await screen.findByRole('heading', { name: '模型与属性字段配置' })
    await userEvent.click(screen.getByRole('button', { name: /可选（optional）/ }))
    expect(screen.queryByText(/开关状态（开关 · power）/)).not.toBeInTheDocument()
    expect(screen.getByText(/亮度.*brightness/, { selector: 'strong' })).toBeInTheDocument()
  })

  it('offers a prominent create entry for the selected model', async () => {
    render(<UnifiedModelManager />)
    await screen.findByRole('heading', { name: '模型与属性字段配置' })
    await userEvent.click(screen.getByRole('button', { name: '＋ 新增自定义属性' }))
    expect(screen.getByRole('dialog', { name: '自定义统一模型属性' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '新建自定义属性' })).toBeInTheDocument()
    expect(screen.getByLabelText('设备模型（deviceType）')).toHaveValue('lightbulb')
    expect(screen.getByLabelText('设备模型（deviceType）')).toBeDisabled()
  })

  it('creates a complete database-backed unified model before configuring properties', async () => {
    vi.mocked(api.createCustomModel).mockResolvedValue({ deviceType: 'air-quality-monitor', name: '空气质量监测器', version: 1 })
    vi.mocked(api.listModelContracts)
      .mockResolvedValueOnce([model])
      .mockResolvedValueOnce([model, { deviceType: 'air-quality-monitor', name: '空气质量监测器', version: 1, builtIn: false, parameters: [], custom: model.custom }])
    render(<UnifiedModelManager />)
    await screen.findByRole('heading', { name: '模型与属性字段配置' })
    await userEvent.click(screen.getByRole('button', { name: '＋ 新增统一模型' }))
    await userEvent.type(screen.getByPlaceholderText('air-quality-monitor'), 'air-quality-monitor')
    await userEvent.type(screen.getByPlaceholderText('空气质量监测器'), '空气质量监测器')
    await userEvent.click(screen.getByRole('button', { name: '创建并配置属性' }))
    expect(api.createCustomModel).toHaveBeenCalledWith({ deviceType: 'air-quality-monitor', name: '空气质量监测器', version: 1 })
    expect(await screen.findByRole('heading', { name: '空气质量监测器（air-quality-monitor）' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '＋ 新增自定义属性' })).toBeInTheDocument()
  })
})
