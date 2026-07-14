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
    await userEvent.click(screen.getByRole('button', { name: '＋ 新建自定义属性' }))
    await userEvent.type(screen.getByPlaceholderText('custom-air-co2'), 'switch-vendor-led')
    const fields = screen.getAllByRole('group')
    const property = fields.find((item) => within(item).queryByText('第三级 · Property'))!
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
})
