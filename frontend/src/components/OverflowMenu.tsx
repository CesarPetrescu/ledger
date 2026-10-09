import { useEffect, useRef, useState } from 'react'
import { Icon } from './ui'

export interface OverflowItem {
  label: string
  onSelect: () => void
  danger?: boolean
  disabled?: boolean
}

/** A "⋯" button holding secondary actions, such as Delete, so they stay out of the main row. Escape, a click
 * outside, or focus moving elsewhere closes it, and Escape stops there instead of also closing the panel around it;
 * the menu's first item takes focus when it opens. Choosing an item hands focus back to
 * the button first, so a confirmation dialog it opens returns focus there when it closes. */
export function OverflowMenu({ label, items }: { label: string; items: OverflowItem[] }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    root.current?.querySelector<HTMLButtonElement>('[role="menuitem"]:not([disabled])')?.focus()
    const outside = (event: MouseEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); setOpen(false); button.current?.focus() }
    }
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', key) }
  }, [open])

  return (
    // Only focus landing outside closes it: a click that focuses nothing (Safari buttons) is left to the mousedown check.
    <div className="overflow-menu" ref={root} onBlur={(event) => { if (event.relatedTarget instanceof Node && !root.current?.contains(event.relatedTarget)) setOpen(false) }}>
      <button ref={button} type="button" className="icon-button" aria-label={label} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((current) => !current)}>
        <Icon name="dots" />
      </button>
      {open && (
        <div role="menu" aria-label={label} className="overflow-menu-list">
          {items.map((item) => (
            <button key={item.label} type="button" role="menuitem" className={item.danger ? 'overflow-menu-danger' : undefined} disabled={item.disabled}
              onClick={() => { setOpen(false); button.current?.focus(); item.onSelect() }}>
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
