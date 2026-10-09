import { useEffect, useId, useRef, useState, type ReactNode } from 'react'

/** What each label on an entry means, in the order they appear on a row. */
export const LABEL_GUIDE: { label: string; attrs: Record<string, string>; meaning: string }[] = [
  { label: 'Asks you', attrs: { 'data-focus': 'ask' }, meaning: 'An agent asked you a question or needs something from you. It stays in your Inbox until you answer it or mark it handled.' },
  { label: 'Important', attrs: { 'data-focus': 'important' }, meaning: 'The AI rated it important: a decision that changes direction, a blocker, a production problem, a deadline, or something you must act on.' },
  { label: 'High / Low', attrs: { 'data-priority': 'high' }, meaning: 'A todo’s priority, read from its text: high when urgent, blocking, or broken; low when nice to have. A row shows at most one of High, Important, and Low, preferring them in that order.' },
  { label: 'S / M / L', attrs: { 'data-focus': 'size' }, meaning: 'A todo’s estimated size: under an hour, about a day, or several days.' },
  { label: 'Due / Overdue', attrs: { 'data-focus': 'due' }, meaning: 'A deadline stated in the todo. Overdue once the date has passed.' },
  { label: 'Stale', attrs: { 'data-focus': 'stale' }, meaning: 'A todo that has been open for more than 14 days.' },
  { label: 'Blocked · In progress · Done', attrs: { 'data-state': 'blocked' }, meaning: 'The state a status update reports. A project whose latest update is blocked is listed under Blocked in the Inbox.' },
  { label: 'Check', attrs: { 'data-focus': 'unsure' }, meaning: 'The AI was unsure about some of the labels. Open the entry and correct them; saving a label unchanged confirms it.' },
]

export function LabelLegend() {
  return (
    <dl className="label-legend">
      {LABEL_GUIDE.map((item) => (
        <div key={item.label}>
          <dt><span className="badge" {...item.attrs}>{item.label}</span></dt>
          <dd>{item.meaning}</dd>
        </div>
      ))}
    </dl>
  )
}

/** A small "?" link that explains the labels in a dialog. */
export function LegendButton() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className="link-button legend-button" onClick={() => setOpen(true)}>What do the labels mean?</button>
      <InfoDialog open={open} title="What the labels mean" onClose={() => setOpen(false)}>
        <LabelLegend />
      </InfoDialog>
    </>
  )
}

/** A native modal with only a Close button. */
function InfoDialog({ open, title, onClose, children }: { open: boolean; title: string; onClose: () => void; children: ReactNode }) {
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
  return (
    <dialog ref={ref} className="dialog dialog-wide" aria-labelledby={titleId} onCancel={(event) => { event.preventDefault(); onClose() }}>
      <form method="dialog" onSubmit={(event) => { event.preventDefault(); onClose() }}>
        <h2 id={titleId}>{title}</h2>
        <div className="dialog-body">{children}</div>
        <div className="dialog-actions">
          <button type="submit" className="btn btn-primary" autoFocus>Close</button>
        </div>
      </form>
    </dialog>
  )
}
