import { useRef, useState } from 'react'

export type ToastKind = 'success' | 'error' | 'info'
export interface ToastMessage { id: number; kind: ToastKind; message: string }

export function useToasts() {
  const [toasts, setToasts] = useState<ToastMessage[]>([]); const nextID = useRef(0)
  const dismiss = (id: number) => setToasts((current) => current.filter((item) => item.id !== id))
  const notify = (kind: ToastKind, message: string) => { nextID.current += 1; const toast = { id: nextID.current, kind, message }; setToasts((current) => [...current.slice(-3), toast]) }
  return { toasts, notify, dismiss }
}
