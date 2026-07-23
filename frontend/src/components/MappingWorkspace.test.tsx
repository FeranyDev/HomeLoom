import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { MappingWorkspace } from './MappingWorkspace'

vi.mock('./UnifiedModelManager', () => ({ UnifiedModelManager: () => <div>统一模型目录内容</div> }))
vi.mock('./ProfileManager', () => ({ ProfileManager: () => <div>转换配置管理内容</div> }))
vi.mock('./MappingPreview', () => ({ MappingPreview: () => <div>转换预览内容</div> }))

describe('MappingWorkspace', () => {
  it('uses unified models as the primary page and keeps conversion tools secondary', async () => {
    render(<MappingWorkspace />)
    expect(screen.getByText('统一模型目录内容')).toBeInTheDocument()
    expect(screen.queryByText('转换配置管理内容')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: /转换配置/ }))
    expect(screen.getByText('转换配置管理内容')).toBeInTheDocument()
    expect(screen.getByText('转换预览内容')).toBeInTheDocument()
    expect(screen.getByRole('banner', { name: '转换配置工作台' })).toBeInTheDocument()
    expect(screen.getByRole('list', { name: '转换配置使用流程' })).toHaveTextContent(/识别差异.*编排规则.*绑定设备/)
    expect(screen.getByRole('complementary', { name: '转换配置应用位置' })).toHaveTextContent(/设备中心.*桥接中心/)
    expect(screen.getByRole('region', { name: '验证数据库中的 Profile' })).toBeInTheDocument()
  })
})
