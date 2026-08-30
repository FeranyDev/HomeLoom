import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { HelpTooltip } from './HelpTooltip'

describe('HelpTooltip', () => {
  afterEach(() => vi.restoreAllMocks())
  it('shows concise help on hover and hides it afterwards', async () => {
    const user = userEvent.setup()
    render(<HelpTooltip label="查看连接说明" content="保存后会立即重新连接。" />)

    const trigger = screen.getByRole('button', { name: '查看连接说明' })
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    await user.hover(trigger)
    expect(screen.getByRole('tooltip')).toHaveTextContent('保存后会立即重新连接。')
    expect(trigger).toHaveAttribute('aria-describedby')

    await user.unhover(trigger)
    await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument())
  })

  it('keeps the help open while moving from the trigger into the bubble', async () => {
    const user = userEvent.setup()
    render(<HelpTooltip content="可悬浮阅读。">说明</HelpTooltip>)
    const trigger = screen.getByRole('button', { name: '查看说明' })
    await user.hover(trigger)
    const bubble = screen.getByRole('tooltip')
    await user.hover(bubble)
    await act(() => new Promise((resolve) => setTimeout(resolve, 150)))
    expect(bubble).toBeInTheDocument()
    await user.unhover(bubble)
    await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument())
  })

  it('opens on click without toggling the labelled checkbox and dismisses outside', async () => {
    const user = userEvent.setup()
    render(<label><input type="checkbox" aria-label="启用回读" /><HelpTooltip content="写入后读取状态。">写后回读</HelpTooltip></label>)
    await user.click(screen.getByRole('button', { name: '查看说明' }))
    expect(screen.getByRole('tooltip')).toHaveTextContent('写入后读取状态。')
    expect(screen.getByRole('checkbox')).not.toBeChecked()
    await user.click(document.body)
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    await user.click(screen.getByText('写后回读'))
    expect(screen.getByRole('checkbox')).toBeChecked()
  })

  it('portals outside clipped containers and follows the trigger on scroll and resize', async () => {
    let anchor = new DOMRect(220, 160, 15, 15)
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      if (this.classList.contains('help-tooltip__trigger')) return anchor
      if (this.classList.contains('help-tooltip__bubble')) return new DOMRect(0, 0, 200, 60)
      return new DOMRect(0, 0, window.innerWidth, window.innerHeight)
    })
    const user = userEvent.setup()
    const { container, unmount } = render(<div style={{ overflow: 'hidden' }}><HelpTooltip content="不会被边框遮挡。">映射</HelpTooltip></div>)
    await user.hover(screen.getByRole('button', { name: '查看说明' }))
    const bubble = screen.getByRole('tooltip')
    expect(bubble.parentElement).toBe(document.body)
    expect(container).not.toContainElement(bubble)
    expect(bubble).toHaveAttribute('data-side', 'top')
    expect(bubble).toHaveStyle({ left: '127.5px', top: '94px' })
    anchor = new DOMRect(2, 5, 15, 15)
    fireEvent.scroll(container.firstChild!)
    expect(bubble).toHaveAttribute('data-side', 'bottom')
    expect(bubble).toHaveStyle({ left: '8px', top: '26px' })
    anchor = new DOMRect(window.innerWidth - 18, 160, 15, 15)
    fireEvent.resize(window)
    expect(bubble).toHaveStyle({ left: `${window.innerWidth - 208}px`, top: '94px' })
    anchor = new DOMRect(220, -30, 15, 15)
    fireEvent.scroll(container.firstChild!)
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    unmount()
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('supports keyboard focus and Escape', async () => {
    const user = userEvent.setup()
    render(<HelpTooltip content="仅影响当前设备。" />)

    await user.tab()
    expect(screen.getByRole('tooltip')).toHaveTextContent('仅影响当前设备。')
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    await user.keyboard(' ')
    expect(screen.getByRole('tooltip')).toHaveTextContent('仅影响当前设备。')
    await user.keyboard('{Escape}{Enter}')
    expect(screen.getByRole('tooltip')).toHaveTextContent('仅影响当前设备。')
  })
})
