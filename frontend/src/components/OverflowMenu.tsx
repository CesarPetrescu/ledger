import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { Icon } from './ui'

export interface OverflowItem {
  label: string
  onSelect: () => void
  danger?: boolean
  disabled?: boolean
}

/** A "⋯" button holding secondary actions, such as Delete, so they stay out of the main row. Escape, a click
 * outside, or focus moving elsewhere closes it, and Escape stops there instead of also closing the panel around it;
 * the menu's first item takes focus when it opens, and the arrow keys, Home, and End move between items. Choosing
 * an item hands focus back to the button first, so a confirmation dialog it opens returns focus there when it closes. */
export function OverflowMenu({ label, items, text }: { label: string; items: OverflowItem[]; text?: string }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const button = useRef<HTMLButtonElement>(null)

  // The list opens leftward from its button; a button near the left edge (a phone row's actions start there) would
  // push it off screen, so it opens rightward instead. Measured before paint, on the list each opening mounts afresh.
  useLayoutEffect(() => {
    const list = root.current?.querySelector<HTMLElement>('.overflow-menu-list')
    if (list && list.getBoundingClientRect().left < 8) list.dataset.align = 'start'
  }, [open])

  useEffect(() => {
    if (!open) return
    root.current?.querySelector<HTMLButtonElement>('[role="menuitem"]:not([disabled])')?.focus()
    const outside = (event: MouseEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); setOpen(false); button.current?.focus(); return }
      // Arrow keys, Home, and End move between the items and stop here, so the list behind (such as the
      // entry list's arrow navigation) does not also act on them.
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
      const choices = [...(root.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not([disabled])') ?? [])]
      if (choices.length === 0) return
      event.preventDefault()
      const at = choices.indexOf(document.activeElement as HTMLButtonElement)
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? choices.length - 1
        : event.key === 'ArrowDown' ? (at + 1) % choices.length : (at - 1 + choices.length) % choices.length
      choices[next]?.focus()
    }
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', key) }
  }, [open])

  return (
    // Only focus landing outside closes it: a click that focuses nothing (Safari buttons) is left to the mousedown check.
    <div className="overflow-menu" ref={root} onBlur={(event) => { if (event.relatedTarget instanceof Node && !root.current?.contains(event.relatedTarget)) setOpen(false) }}>
      {/* With [text] the button reads as a named action, such as Snooze, instead of "⋯". */}
      <button ref={button} type="button" className={text ? 'btn btn-small' : 'icon-button'} aria-label={text ? undefined : label} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((current) => !current)}>
        {text ?? <Icon name="dots" />}
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
