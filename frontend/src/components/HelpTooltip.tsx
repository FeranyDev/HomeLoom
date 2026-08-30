import { useId, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

interface HelpTooltipProps {
  content: string
  label?: string
  children?: ReactNode
}

/** A short, keyboard-accessible explanation that does not crowd the page. */
export function HelpTooltip({ content, label = '查看说明', children }: HelpTooltipProps) {
  const [open, setOpen] = useState(false)
  const [position, setPosition] = useState<{ left: number; top: number; arrow: number; side: 'top' | 'bottom' } | null>(null)
  const triggerRef = useRef<HTMLSpanElement>(null)
  const bubbleRef = useRef<HTMLSpanElement>(null)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const id = useId()

  const cancelClose = () => clearTimeout(closeTimer.current)
  const show = () => { cancelClose(); setOpen(true) }
  const hide = () => { cancelClose(); setOpen(false); setPosition(null) }
  const leave = () => {
    cancelClose()
    closeTimer.current = setTimeout(() => {
      if (document.activeElement !== triggerRef.current) hide()
    }, 120)
  }

  useLayoutEffect(() => {
    if (!open) return
    const update = () => {
      const trigger = triggerRef.current
      const bubble = bubbleRef.current
      if (!trigger || !bubble) return
      const rect = trigger.getBoundingClientRect()
      const width = bubble.getBoundingClientRect().width
      const height = bubble.getBoundingClientRect().height
      const viewport = window.visualViewport
      const minX = (viewport?.offsetLeft ?? 0) + 8
      const minY = (viewport?.offsetTop ?? 0) + 8
      const maxX = minX + (viewport?.width ?? window.innerWidth) - 16
      const maxY = minY + (viewport?.height ?? window.innerHeight) - 16
      const outside = (bounds: { left: number; right: number; top: number; bottom: number }, clipX = true, clipY = true) =>
        (clipX && (rect.right <= bounds.left || rect.left >= bounds.right)) || (clipY && (rect.bottom <= bounds.top || rect.top >= bounds.bottom))
      let clipped = outside({ left: minX - 8, right: maxX + 8, top: minY - 8, bottom: maxY + 8 })
      for (let parent = trigger.parentElement; parent && parent !== document.body && !clipped; parent = parent.parentElement) {
        const style = getComputedStyle(parent)
        const clipX = /auto|scroll|hidden|clip/.test(style.overflowX || style.overflow)
        const clipY = /auto|scroll|hidden|clip/.test(style.overflowY || style.overflow)
        if (clipX || clipY) clipped = outside(parent.getBoundingClientRect(), clipX, clipY)
      }
      if (rect.width > 0 && rect.height > 0 && clipped) { setOpen(false); setPosition(null); return }
      const center = rect.left + rect.width / 2
      const left = Math.max(minX, Math.min(center - width / 2, maxX - width))
      const side = rect.top - height - 6 >= minY || rect.top - minY > maxY - rect.bottom ? 'top' : 'bottom'
      const top = Math.max(minY, Math.min(side === 'top' ? rect.top - height - 6 : rect.bottom + 6, maxY - height))
      setPosition({ left, top, arrow: Math.max(10, Math.min(center - left, width - 10)), side })
    }
    const dismiss = (event: PointerEvent) => {
      if (!triggerRef.current?.contains(event.target as Node) && !bubbleRef.current?.contains(event.target as Node)) {
        setOpen(false)
        setPosition(null)
      }
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setOpen(false); setPosition(null) }
    }
    update()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(update)
    if (triggerRef.current) observer?.observe(triggerRef.current)
    if (bubbleRef.current) observer?.observe(bubbleRef.current)
    window.addEventListener('scroll', update, true)
    window.addEventListener('resize', update)
    window.visualViewport?.addEventListener('resize', update)
    window.visualViewport?.addEventListener('scroll', update)
    document.addEventListener('pointerdown', dismiss)
    document.addEventListener('keydown', escape)
    return () => {
      clearTimeout(closeTimer.current)
      observer?.disconnect()
      window.removeEventListener('scroll', update, true)
      window.removeEventListener('resize', update)
      window.visualViewport?.removeEventListener('resize', update)
      window.visualViewport?.removeEventListener('scroll', update)
      document.removeEventListener('pointerdown', dismiss)
      document.removeEventListener('keydown', escape)
    }
  }, [open, content])

  // A non-labelable trigger keeps an enclosing <label> linked to its form field.
  const trigger = <span className="help-tooltip">
    <span
      ref={triggerRef}
      role="button"
      tabIndex={0}
      className="help-tooltip__trigger"
      aria-label={label}
      aria-describedby={open ? id : undefined}
      onMouseEnter={show}
      onMouseLeave={leave}
      onFocus={show}
      onBlur={hide}
      onClick={(event) => { event.preventDefault(); event.stopPropagation(); show() }}
      onKeyDown={(event) => {
        if (event.key === 'Escape') { event.stopPropagation(); hide() }
        if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); event.stopPropagation(); show() }
      }}
    ><span aria-hidden="true">?</span></span>
    {open && createPortal(<span
      ref={bubbleRef}
      id={id}
      className="help-tooltip__bubble"
      role="tooltip"
      data-side={position?.side ?? 'top'}
      style={{ left: position?.left ?? 0, top: position?.top ?? 0, visibility: position ? 'visible' : 'hidden', '--help-arrow-left': `${position?.arrow ?? 0}px` } as CSSProperties}
      onMouseEnter={cancelClose}
      onMouseLeave={leave}
      onClick={(event) => event.stopPropagation()}
    ><span className="help-tooltip__content">{content}</span></span>, document.body)}
  </span>

  return children ? <span className="help-label"><span className="help-label__text">{children}</span>{trigger}</span> : trigger
}
