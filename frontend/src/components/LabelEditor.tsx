import { useState, type FormEvent } from 'react'
import { api, describeError, type LabelField, type LabelPatch, type TableEntry } from '../api'
import { useToast } from './Toast'

interface FieldSpec {
  field: LabelField
  label: string
  kinds?: string[]
  options?: string[]
  type?: 'date' | 'long'
}

const FIELDS: FieldSpec[] = [
  { field: 'title', label: 'Title' },
  { field: 'importance', label: 'Importance', options: ['routine', 'useful', 'important'] },
  { field: 'gist', label: 'Summary', type: 'long' },
  { field: 'category', label: 'Category' },
  { field: 'tags', label: 'Tags (comma separated)' },
  { field: 'ask', label: 'Asks you' },
  { field: 'priority', label: 'Priority', kinds: ['todo'], options: ['low', 'normal', 'high'] },
  { field: 'size', label: 'Size', kinds: ['todo'], options: ['', 'S', 'M', 'L'] },
  { field: 'due', label: 'Due', kinds: ['todo'], type: 'date' },
  { field: 'state', label: 'State', kinds: ['status'], options: ['', 'done', 'in_progress', 'blocked'] },
  { field: 'next_step', label: 'Next step' },
  { field: 'blocker', label: 'Blocked by' },
  { field: 'why', label: 'Why', type: 'long' },
]

const shown = (value: string) => (value === '' ? '—' : value.replace('_', ' '))

/** Lets the owner correct the AI's labels. Corrections are kept through
 * re-extraction and teach the labeller for similar entries. */
export function LabelEditor({ entry, onChanged }: { entry: TableEntry; onChanged: () => void }) {
  const meta = entry.meta
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()
  const fields = FIELDS.filter((spec) => !spec.kinds || spec.kinds.includes(entry.kind))
  const current = (field: LabelField) => {
    const value = meta?.[field]
    return Array.isArray(value) ? value.join(', ') : (value ?? '')
  }
  const [values, setValues] = useState<Record<string, string>>({})
  if (!meta || meta.origin !== 'model') return null

  const start = () => {
    setValues(Object.fromEntries(fields.map((spec) => [spec.field, current(spec.field)])))
    setError('')
    setOpen(true)
  }
  const save = async (set: LabelPatch, reset: LabelField[], message: string) => {
    setBusy(true)
    setError('')
    try {
      await api.setLabels(entry.id, set, reset)
      setOpen(false)
      toast(message)
      onChanged()
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const set: Record<string, string | string[]> = {}
    for (const spec of fields) {
      const value = (values[spec.field] ?? '').trim()
      if (value === current(spec.field)) continue
      set[spec.field] = spec.field === 'tags' ? value.split(/[,\s]+/).filter(Boolean) : value
    }
    if (Object.keys(set).length === 0) {
      setOpen(false)
      return
    }
    void save(set as LabelPatch, [], 'Labels saved. Similar entries will be labelled this way.')
  }

  if (!open) {
    return (
      <p className="label-status">
        <button type="button" className="link-button" onClick={start}>Edit labels</button>
        {meta.edited && meta.edited.length > 0 && <span className="muted small">Edited by you: {meta.edited.map((field) => FIELDS.find((spec) => spec.field === field)?.label ?? field).join(', ')}</span>}
      </p>
    )
  }
  return (
    <form className="form label-editor" aria-label="Edit labels" onSubmit={submit}>
      <div className="form-grid">
        {fields.map((spec) => (
          <label key={spec.field} className={spec.type === 'long' || spec.field === 'title' ? 'span-2' : undefined}>
            {spec.label}
            {meta.unsure?.includes(spec.field) && <span className="badge" data-focus="unsure"> AI unsure</span>}
            {spec.options ? (
              <select value={values[spec.field]} onChange={(event) => setValues({ ...values, [spec.field]: event.target.value })}>
                {spec.options.map((option) => <option key={option} value={option}>{shown(option)}</option>)}
              </select>
            ) : spec.type === 'long' ? (
              <textarea rows={2} value={values[spec.field]} maxLength={300} onChange={(event) => setValues({ ...values, [spec.field]: event.target.value })} />
            ) : (
              <input type={spec.type === 'date' ? 'date' : 'text'} value={values[spec.field]} maxLength={spec.field === 'category' ? 40 : 300}
                required={spec.field === 'title'} onChange={(event) => setValues({ ...values, [spec.field]: event.target.value })} />
            )}
          </label>
        ))}
      </div>
      {error && <p className="field-error" role="alert">{error}</p>}
      <div className="form-actions">
        {meta.edited && meta.edited.length > 0 && (
          <button type="button" className="link-button" disabled={busy} onClick={() => void save({}, meta.edited ?? [], "Back to the AI's labels.")}>
            Reset to AI labels
          </button>
        )}
        <button type="button" className="btn" disabled={busy} onClick={() => setOpen(false)}>Cancel</button>
        <button type="submit" className="btn btn-primary" disabled={busy}>{busy ? 'Saving…' : 'Save labels'}</button>
      </div>
    </form>
  )
}
