import { useCallback, useEffect, useRef, useState } from 'react'
import { CloseIcon } from './icons'

export function Sheet({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  const [phase, setPhase] = useState<'opening' | 'open' | 'closing'>('opening')
  const [dragOffset, setDragOffset] = useState(0)
  const [dragging, setDragging] = useState(false)
  const sheetRef = useRef<HTMLElement>(null)
  const phaseRef = useRef(phase)
  const closeTimerRef = useRef<number | null>(null)
  const closedRef = useRef(false)
  const dragRef = useRef<{
    pointerID: number
    startY: number
    lastY: number
    lastTime: number
    velocity: number
  } | null>(null)

  const finishClose = useCallback(() => {
    if (closedRef.current) return
    closedRef.current = true
    onClose()
  }, [onClose])

  const requestClose = useCallback(() => {
    if (phaseRef.current === 'closing') return
    phaseRef.current = 'closing'
    dragRef.current = null
    setDragging(false)
    setDragOffset(0)
    setPhase('closing')
    closeTimerRef.current = window.setTimeout(finishClose, 300)
  }, [finishClose])

  useEffect(() => {
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const frame = window.requestAnimationFrame(() => {
      if (phaseRef.current !== 'opening') return
      phaseRef.current = 'open'
      setPhase('open')
    })
    return () => {
      window.cancelAnimationFrame(frame)
      if (closeTimerRef.current !== null) window.clearTimeout(closeTimerRef.current)
      document.body.style.overflow = previousOverflow
    }
  }, [])

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !document.querySelector('[role="alertdialog"]')) requestClose()
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [requestClose])

  function startDrag(event: React.PointerEvent<HTMLDivElement>) {
    if (phaseRef.current !== 'open' || !event.isPrimary || event.button !== 0) return
    dragRef.current = {
      pointerID: event.pointerId,
      startY: event.clientY,
      lastY: event.clientY,
      lastTime: event.timeStamp,
      velocity: 0,
    }
    event.currentTarget.setPointerCapture(event.pointerId)
    setDragging(true)
  }

  function moveDrag(event: React.PointerEvent<HTMLDivElement>) {
    const drag = dragRef.current
    if (!drag || drag.pointerID !== event.pointerId) return
    const elapsed = event.timeStamp - drag.lastTime
    if (elapsed > 0) drag.velocity = (event.clientY - drag.lastY) / elapsed
    drag.lastY = event.clientY
    drag.lastTime = event.timeStamp
    setDragOffset(Math.max(0, event.clientY - drag.startY))
  }

  function endDrag(event: React.PointerEvent<HTMLDivElement>, cancelled = false) {
    const drag = dragRef.current
    if (!drag || drag.pointerID !== event.pointerId) return
    const offset = Math.max(0, event.clientY - drag.startY)
    const threshold = Math.min(140, (sheetRef.current?.offsetHeight ?? 560) * 0.25)
    dragRef.current = null
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
    if (!cancelled && (offset >= threshold || (offset > 24 && drag.velocity > 0.55))) {
      requestClose()
      return
    }
    setDragging(false)
    setDragOffset(0)
  }

  return (
    <div
      className={`sheet-backdrop sheet-${phase}${dragging ? ' sheet-dragging' : ''}`}
      role="presentation"
      onPointerDown={(event) => event.target === event.currentTarget && requestClose()}
    >
      <section
        className="sheet"
        ref={sheetRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="sheet-title"
        style={{ '--sheet-drag-y': `${dragOffset}px` } as React.CSSProperties}
        onTransitionEnd={(event) => {
          if (event.target === event.currentTarget && event.propertyName === 'transform' && phaseRef.current === 'closing') finishClose()
        }}
      >
        <div
          className="sheet-grab-area"
          aria-hidden="true"
          onPointerDown={startDrag}
          onPointerMove={moveDrag}
          onPointerUp={(event) => endDrag(event)}
          onPointerCancel={(event) => endDrag(event, true)}
        >
          <div className="sheet-handle" />
        </div>
        <div className="sheet-heading"><h2 id="sheet-title">{title}</h2><button type="button" onClick={requestClose} aria-label="Close"><CloseIcon /></button></div>
        {children}
      </section>
    </div>
  )
}
