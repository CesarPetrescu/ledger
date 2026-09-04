import { useEffect, useId, useRef, type FormEvent, type ReactNode } from 'react'

interface Props {
  open: boolean
  title: string
  confirmLabel: string
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
  children: ReactNode
}

/** Native modal dialog: focus is trapped by the platform and Escape cancels. */
export function ConfirmDialog({ open, title, confirmLabel, busy = false, onConfirm, onCancel, children }: Props) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleId = useId()

  useEffect(() => {
    const dialog = ref.current
    if (!dialog || !open) return
    if (typeof dialog.showModal === 'function') {
      if (!dialog.open) dialog.showModal()
    } else {
      dialog.setAttribute('open', '')
    }
    return () => {
      if (dialog.open && typeof dialog.close === 'function') dialog.close()
    }
  }, [open])

  if (!open) return null

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!busy) onConfirm()
  }

  return (
    <dialog
      ref={ref}
      className="dialog"
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault()
        onCancel()
      }}
    >
      <form onSubmit={submit}>
        <h2 id={titleId}>{title}</h2>
        <div className="dialog-body">{children}</div>
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button type="submit" className="btn btn-danger" disabled={busy} autoFocus>
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </form>
    </dialog>
  )
}
