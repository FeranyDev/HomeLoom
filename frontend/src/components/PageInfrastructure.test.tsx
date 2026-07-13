import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ErrorBoundary } from './ErrorBoundary'
import { ToastCenter } from './ToastCenter'
import { useToasts } from '../useToasts'
import type { ReactNode } from 'react'

afterEach(() => vi.restoreAllMocks())

function ToastHarness() { const { toasts, notify, dismiss } = useToasts(); return <><button onClick={() => notify('success', '保存成功')}>通知</button><ToastCenter toasts={toasts} dismiss={dismiss} /></> }
function Broken(): ReactNode { throw new Error('render failed') }

describe('page infrastructure', () => {
  it('shows and dismisses toast notifications', async () => {
    render(<ToastHarness />); await userEvent.click(screen.getByRole('button', { name: '通知' })); expect(screen.getByRole('status')).toHaveTextContent('保存成功'); await userEvent.click(screen.getByRole('button', { name: '关闭通知' })); expect(screen.queryByText('保存成功')).not.toBeInTheDocument()
  })

  it('contains rendering failures', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {}); render(<ErrorBoundary><Broken /></ErrorBoundary>); expect(screen.getByText('控制台遇到了问题。')).toBeInTheDocument(); expect(screen.getByText('render failed')).toBeInTheDocument()
  })
})
