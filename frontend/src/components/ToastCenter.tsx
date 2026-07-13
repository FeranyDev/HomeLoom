import { useEffect } from 'react'
import type { ToastMessage } from '../useToasts'

function ToastItem({ toast, dismiss }: { toast: ToastMessage; dismiss: (id: number) => void }) {
  useEffect(() => { const timer = window.setTimeout(() => dismiss(toast.id), toast.kind === 'error' ? 6500 : 4000); return () => window.clearTimeout(timer) }, [dismiss, toast])
  return <div className={`toast is-${toast.kind}`} role={toast.kind === 'error' ? 'alert' : 'status'}><span>{toast.message}</span><button aria-label="关闭通知" onClick={() => dismiss(toast.id)}>×</button></div>
}

export function ToastCenter({ toasts, dismiss }: { toasts: ToastMessage[]; dismiss: (id: number) => void }) { return <aside className="toast-center" aria-label="通知">{toasts.map((toast) => <ToastItem key={toast.id} toast={toast} dismiss={dismiss} />)}</aside> }
