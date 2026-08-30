import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CustomModelPropertyManager } from './CustomModelPropertyManager'
import * as api from '../api/mapping'

vi.mock('../api/mapping', () => ({
  listCustomModelProperties: vi.fn(), createCustomModelProperty: vi.fn(),
  updateCustomModelProperty: vi.fn(), deleteCustomModelProperty: vi.fn(),
}))

describe('CustomModelPropertyManager', () => {
  beforeEach(() => {
    vi.mocked(api.listCustomModelProperties).mockResolvedValue([])
    vi.mocked(api.createCustomModelProperty).mockImplementation(async (item) => item)
  })

  it('creates a three-level custom unified property visually', async () => {
    const changed = vi.fn()
    render(<CustomModelPropertyManager onChanged={changed} />)
    await screen.findByText(/还没有自定义模型属性/)
    await userEvent.click(screen.getByRole('button', { name: '＋ 新建属性' }))
    await userEvent.type(screen.getByPlaceholderText('custom-air-co2'), 'switch-vendor-led')
    const fields = screen.getAllByRole('group')
    const property = fields.find((item) => within(item).queryByText(/第三级.*Property/))!
    const inputs = within(property).getAllByRole('textbox')
    await userEvent.type(inputs[0], 'led-pattern')
    await userEvent.type(inputs[1], 'LED Pattern')
    await userEvent.click(screen.getByRole('button', { name: '保存自定义属性' }))
    await waitFor(() => expect(api.createCustomModelProperty).toHaveBeenCalledWith(expect.objectContaining({
      id: 'switch-vendor-led', deviceType: 'switch', endpointId: 'main', capabilityId: 'custom',
      definition: expect.objectContaining({ id: 'led-pattern', name: 'LED Pattern', parameterLevel: 'custom' }),
    })))
    expect(changed).toHaveBeenCalled()
  })

  it('opens creation from an external model-level action', async () => {
    const { rerender } = render(<CustomModelPropertyManager deviceType="fan" onChanged={vi.fn()} createRevision={0} />)
    await screen.findByText(/这个模型还没有自定义属性/)
    rerender(<CustomModelPropertyManager deviceType="fan" onChanged={vi.fn()} createRevision={1} />)
    expect(screen.getByRole('dialog', { name: '自定义统一模型属性' })).toBeInTheDocument()
    expect(screen.getByLabelText('设备模型（deviceType）')).toHaveValue('fan')
    expect(screen.getByLabelText('设备模型（deviceType）')).toBeDisabled()
    rerender(<CustomModelPropertyManager deviceType="switch" onChanged={vi.fn()} createRevision={1} />)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '自定义统一模型属性' })).not.toBeInTheDocument())
  })

  it('shows only the constraints supported by the selected value type', async () => {
    render(<CustomModelPropertyManager onChanged={vi.fn()} />)
    await screen.findByText(/还没有自定义模型属性/)
    await userEvent.click(screen.getByRole('button', { name: '＋ 新建属性' }))

    const valueType = screen.getByLabelText('值类型（type）')
    expect(screen.queryByLabelText('单位（unit）')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('最小值（min）')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('枚举值（enum）')).not.toBeInTheDocument()

    await userEvent.selectOptions(valueType, 'number')
    expect(screen.getByLabelText('单位（unit）')).toBeInTheDocument()
    expect(screen.getByLabelText('最小值（min）')).toBeInTheDocument()
    expect(screen.getByLabelText('最大值（max）')).toBeInTheDocument()
    expect(screen.getByLabelText('步长（step）')).toBeInTheDocument()

    await userEvent.selectOptions(valueType, 'enum')
    expect(screen.getByLabelText('枚举值（enum）')).toBeInTheDocument()
    expect(screen.queryByLabelText('单位（unit）')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('最小值（min）')).not.toBeInTheDocument()

    await userEvent.selectOptions(valueType, 'bool')
    expect(screen.queryByLabelText('枚举值（enum）')).not.toBeInTheDocument()
  })
})
