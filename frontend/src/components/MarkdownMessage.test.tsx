import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MarkdownMessage } from './MarkdownMessage'

describe('MarkdownMessage', () => {
  it('renders common Markdown while excluding raw HTML and unsafe links', () => {
    render(<MarkdownMessage content={'## 处理结果\n\n- 已完成 **配置**\n\n| 项目 | 状态 |\n| --- | --- |\n| 灯光 | 正常 |\n\n[帮助](https://example.com/help) [危险](javascript:alert(1))\n\n<script>alert(1)</script>'} />)

    expect(screen.getByRole('heading', { name: '处理结果' })).toBeInTheDocument()
    expect(screen.getByText('配置').tagName).toBe('STRONG')
    expect(screen.getByRole('cell', { name: '灯光' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '帮助' })).toHaveAttribute('href', 'https://example.com/help')
    expect(screen.queryByRole('link', { name: '危险' })).not.toBeInTheDocument()
    expect(screen.queryByText('alert(1)')).not.toBeInTheDocument()
  })
})
