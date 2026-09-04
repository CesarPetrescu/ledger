import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { api, describeError, type CalendarConnection, type CalendarEvent, type CalendarEventInput, type CalendarSource } from '../api'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Icon, Loading, StaleNotice } from '../components/ui'
import { useResource } from '../hooks/useResource'

function today(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
}

function localDate(value: string): Date {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(year!, month! - 1, day!)
}

function localDateKey(value: string): string {
  const date = new Date(value)
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function localDateTime(value: string): string {
  const date = new Date(value)
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

function eventColor(calendarID: string): number {
  let hash = 0
  for (const character of calendarID) hash = (hash * 31 + character.charCodeAt(0)) >>> 0
  return hash % 5
}

function calendarRange(anchor: string, days: number) {
  const start = localDate(anchor)
  const end = new Date(start)
  end.setDate(end.getDate() + days)
  return { start: start.toISOString(), end: end.toISOString() }
}

function ConnectCalendar({ onConnected }: { onConnected: () => void }) {
  const [serverURL, setServerURL] = useState('https://')
  const [flow, setFlow] = useState<{ id: string; loginURL: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()

  useEffect(() => {
    if (!flow) return
    let active = true
    let timer = 0
    const poll = async () => {
      try {
        const connection = await api.pollCalendarLogin(flow.id)
        if (!active) return
        if (connection.pending) timer = window.setTimeout(() => void poll(), 1800)
        else {
          setFlow(null)
          toast('Nextcloud connected.')
          onConnected()
        }
      } catch (failure) {
        if (!active) return
        setError(describeError(failure))
        setFlow(null)
      }
    }
    timer = window.setTimeout(() => void poll(), 1800)
    return () => {
      active = false
      window.clearTimeout(timer)
    }
  }, [flow, onConnected, toast])

  const start = async (event: FormEvent) => {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      const next = await api.startCalendarLogin(serverURL)
      setFlow({ id: next.id, loginURL: next.login_url })
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="calendar-connect" aria-labelledby="calendar-connect-title">
      <p className="eyebrow">Private CalDAV connection</p>
      <h1 id="calendar-connect-title">Connect your Nextcloud calendar</h1>
      <p className="muted">Ledger uses Nextcloud’s secure login flow. Your main password never enters Ledger, and agents only see calendars you select.</p>
      <form onSubmit={(event) => void start(event)}>
        <label>
          Nextcloud server
          <input type="url" value={serverURL} onChange={(event) => setServerURL(event.target.value)} required placeholder="https://cloud.example.com" autoComplete="url" />
        </label>
        <button type="submit" className="btn btn-primary" disabled={busy || flow !== null}>
          {busy ? 'Contacting Nextcloud…' : 'Connect Nextcloud'}
        </button>
      </form>
      {flow && (
        <div className="calendar-auth-wait" role="status">
          <p><strong>Authorization is waiting.</strong> Open Nextcloud, approve Ledger, then return here.</p>
          <a className="btn btn-primary" href={flow.loginURL} target="_blank" rel="noreferrer">
            Open Nextcloud <Icon name="external" />
          </a>
        </div>
      )}
      {error && <p className="field-error" role="alert">{error}</p>}
    </section>
  )
}

interface EventDraft {
  calendarID: string
  title: string
  start: string
  end: string
  allDay: boolean
  location: string
  description: string
}

function emptyDraft(calendarID: string): EventDraft {
  const start = new Date()
  start.setMinutes(0, 0, 0)
  start.setHours(start.getHours() + 1)
  const end = new Date(start.getTime() + 60 * 60 * 1000)
  return { calendarID, title: '', start: localDateTime(start.toISOString()), end: localDateTime(end.toISOString()), allDay: false, location: '', description: '' }
}

function eventDraft(event: CalendarEvent): EventDraft {
  return {
    calendarID: event.calendar_id,
    title: event.title,
    start: event.all_day ? event.start : localDateTime(event.start),
    end: event.all_day ? event.end : localDateTime(event.end),
    allDay: event.all_day,
    location: event.location ?? '',
    description: event.description ?? '',
  }
}

function EventEditor({ event, calendars, onClose, onSaved }: { event: CalendarEvent | null; calendars: CalendarSource[]; onClose: () => void; onSaved: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const [draft, setDraft] = useState<EventDraft>(() => event ? eventDraft(event) : emptyDraft(calendars[0]?.id ?? ''))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const toast = useToast()

  useEffect(() => {
    const element = dialog.current
    if (!element) return
    if (typeof element.showModal === 'function') element.showModal()
    else element.setAttribute('open', '')
    return () => {
      if (element.open && typeof element.close === 'function') element.close()
    }
  }, [])

  const set = <K extends keyof EventDraft>(key: K, value: EventDraft[K]) => setDraft((current) => ({ ...current, [key]: value }))

  const input = (): CalendarEventInput => ({
    title: draft.title,
    start: draft.allDay ? draft.start : new Date(draft.start).toISOString(),
    end: draft.allDay ? draft.end : new Date(draft.end).toISOString(),
    all_day: draft.allDay,
    location: draft.location,
    description: draft.description,
  })

  const save = async (submitEvent: FormEvent) => {
    submitEvent.preventDefault()
    if (busy) return
    setBusy(true)
    setError('')
    try {
      if (event) await api.updateCalendarEvent(event.id, event.etag, input())
      else await api.createCalendarEvent(draft.calendarID, input())
      toast(event ? 'Event updated.' : 'Event created.')
      onSaved()
    } catch (failure) {
      setError(describeError(failure))
    } finally {
      setBusy(false)
    }
  }

  const remove = async () => {
    if (!event || busy) return
    setBusy(true)
    setError('')
    try {
      await api.deleteCalendarEvent(event.id, event.etag)
      toast('Event deleted.')
      onSaved()
    } catch (failure) {
      setError(describeError(failure))
      setConfirmingDelete(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <dialog ref={dialog} className="dialog event-dialog" aria-labelledby="event-editor-title" onCancel={(cancelEvent) => { cancelEvent.preventDefault(); onClose() }}>
      <form onSubmit={(submitEvent) => void save(submitEvent)}>
        <header className="event-editor-head">
          <div>
            <p className="eyebrow">{event ? 'Calendar event' : 'New calendar event'}</p>
            <h2 id="event-editor-title">{event ? 'Edit event' : 'Add event'}</h2>
          </div>
          <button type="button" className="icon-button" aria-label="Close" onClick={onClose}><Icon name="close" /></button>
        </header>
        <div className="form-grid">
          <label className="span-2">
            Title
            <input value={draft.title} onChange={(changeEvent) => set('title', changeEvent.target.value)} maxLength={200} required autoFocus />
          </label>
          <label>
            Calendar
            <select value={draft.calendarID} onChange={(changeEvent) => set('calendarID', changeEvent.target.value)} disabled={event !== null} required>
              {calendars.map((calendar) => <option key={calendar.id} value={calendar.id}>{calendar.name}</option>)}
            </select>
          </label>
          <label className="calendar-check">
            <input type="checkbox" checked={draft.allDay} onChange={(changeEvent) => {
              const allDay = changeEvent.target.checked
              setDraft((current) => ({ ...current, allDay, start: allDay ? current.start.slice(0, 10) : `${current.start.slice(0, 10)}T09:00`, end: allDay ? current.end.slice(0, 10) : `${current.end.slice(0, 10)}T10:00` }))
            }} />
            All-day event
          </label>
          <label>
            Starts
            <input type={draft.allDay ? 'date' : 'datetime-local'} value={draft.start} onChange={(changeEvent) => set('start', changeEvent.target.value)} required />
          </label>
          <label>
            Ends
            <input type={draft.allDay ? 'date' : 'datetime-local'} value={draft.end} onChange={(changeEvent) => set('end', changeEvent.target.value)} min={draft.start} required />
          </label>
          <label className="span-2">
            Location
            <input value={draft.location} onChange={(changeEvent) => set('location', changeEvent.target.value)} maxLength={500} />
          </label>
          <label className="span-2">
            Description
            <textarea value={draft.description} onChange={(changeEvent) => set('description', changeEvent.target.value)} rows={4} maxLength={4000} />
          </label>
        </div>
        {event?.recurring && <p className="notice">Saving or deleting changes the whole recurring series.</p>}
        {confirmingDelete && <p className="notice notice-danger" role="alert">Delete this event permanently? <button type="button" className="link-button danger" disabled={busy} onClick={() => void remove()}>Yes, delete it</button></p>}
        {error && <p className="field-error" role="alert">{error}</p>}
        <div className="form-actions">
          {event && !confirmingDelete && <button type="button" className="btn btn-danger-quiet" disabled={busy} onClick={() => setConfirmingDelete(true)}><Icon name="trash" /> Delete</button>}
          <button type="button" className="btn" disabled={busy} onClick={onClose}>Cancel</button>
          <button type="submit" className="btn btn-primary" disabled={busy}>{busy ? 'Saving…' : 'Save event'}</button>
        </div>
      </form>
    </dialog>
  )
}

function EventRow({ event, onEdit }: { event: CalendarEvent; onEdit: () => void }) {
  const start = event.all_day ? localDate(event.start) : new Date(event.start)
  const end = event.all_day ? localDate(event.end) : new Date(event.end)
  const time = event.all_day ? 'All day' : `${start.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}–${end.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
  return (
    <li className="calendar-event" data-color={eventColor(event.calendar_id)}>
      <button type="button" onClick={onEdit}>
        <span className="calendar-event-time">{time}</span>
        <span className="calendar-event-main">
          <strong>{event.title || 'Untitled event'}</strong>
          <span>{event.calendar_name}{event.location ? ` · ${event.location}` : ''}{event.recurring ? ' · Recurring' : ''}</span>
        </span>
      </button>
    </li>
  )
}

function CalendarWorkspace({ connection, onDisconnected }: { connection: CalendarConnection; onDisconnected: () => void }) {
  const calendars = useResource(() => api.listCalendars(), 'calendar:sources', 'calendar')
  const [selection, setSelection] = useState<string[] | null>(null)
  const [managing, setManaging] = useState(connection.selected_calendars === 0)
  const [savingSelection, setSavingSelection] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)
  const [anchor, setAnchor] = useState(today())
  const [days, setDays] = useState(7)
  const [calendarFilter, setCalendarFilter] = useState('')
  const [editing, setEditing] = useState<CalendarEvent | 'new' | null>(null)
  const toast = useToast()

  const range = useMemo(() => calendarRange(anchor, days), [anchor, days])
  const events = useResource(() => api.listCalendarEvents(range.start, range.end, calendarFilter), `calendar:events:${range.start}:${range.end}:${calendarFilter}`, 'calendar')
  const selectedCalendars = (calendars.data ?? []).filter((calendar) => calendar.selected)
  const activeSelection = selection ?? selectedCalendars.map((calendar) => calendar.id)

  const grouped = useMemo(() => {
    const groups = new Map<string, CalendarEvent[]>()
    for (const event of events.data ?? []) {
      const date = event.all_day ? event.start : localDateKey(event.start)
      groups.set(date, [...(groups.get(date) ?? []), event])
    }
    return [...groups.entries()]
  }, [events.data])

  const saveSelection = async () => {
    setSavingSelection(true)
    try {
      await api.selectCalendars(activeSelection)
      toast('Calendar access updated.')
      calendars.update((items) => items.map((item) => ({ ...item, selected: activeSelection.includes(item.id) })))
      setSelection(null)
      events.reload()
      setCalendarFilter('')
      setManaging(false)
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setSavingSelection(false)
    }
  }

  const openEvent = async (event: CalendarEvent) => {
    try {
      setEditing(await api.getCalendarEvent(event.id))
    } catch (failure) {
      toast(describeError(failure), 'error')
    }
  }

  const disconnect = async () => {
    setDisconnecting(true)
    try {
      await api.disconnectCalendar()
      toast('Nextcloud disconnected.')
      onDisconnected()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setDisconnecting(false)
    }
  }

  return (
    <>
      <header className="page-head calendar-head">
        <div>
          <p className="eyebrow">Nextcloud · {connection.username}</p>
          <h1>Calendar</h1>
          <p className="muted">Your selected calendars, live from {connection.server_url}.</p>
        </div>
        <div className="calendar-head-actions">
          <button type="button" className="btn" onClick={() => setManaging((open) => !open)}>Calendars</button>
          {selectedCalendars.length > 0 && <button type="button" className="btn btn-primary" onClick={() => setEditing('new')}><Icon name="plus" /> Add event</button>}
        </div>
      </header>

      {managing && (
        <section className="calendar-manage" aria-labelledby="calendar-manage-title">
          <div>
            <h2 id="calendar-manage-title">Agent-visible calendars</h2>
            <p className="muted">Only checked calendars appear here or through MCP.</p>
          </div>
          {calendars.loading && <Loading label="Discovering calendars…" />}
          {!calendars.loading && !calendars.data && <ErrorState message="Couldn't load Nextcloud calendars." onRetry={calendars.reload} />}
          {calendars.data && (
            <fieldset className="calendar-source-list">
              <legend className="visually-hidden">Select calendars</legend>
              {calendars.data.map((calendar) => (
                <label key={calendar.id}>
                  <input type="checkbox" checked={activeSelection.includes(calendar.id)} onChange={(changeEvent) => setSelection(changeEvent.target.checked ? [...activeSelection, calendar.id] : activeSelection.filter((id) => id !== calendar.id))} />
                  <span><strong>{calendar.name}</strong>{calendar.description && <small>{calendar.description}</small>}</span>
                </label>
              ))}
            </fieldset>
          )}
          <div className="form-actions">
            <button type="button" className="btn btn-danger-quiet" onClick={() => setConfirmDisconnect(true)}>Disconnect Nextcloud</button>
            <button type="button" className="btn btn-primary" disabled={!calendars.data || savingSelection} onClick={() => void saveSelection()}>{savingSelection ? 'Saving…' : 'Save calendars'}</button>
          </div>
        </section>
      )}

      {selectedCalendars.length > 0 && (
        <div className="calendar-toolbar">
          <label>
            Starting
            <input type="date" value={anchor} onChange={(changeEvent) => setAnchor(changeEvent.target.value)} />
          </label>
          <fieldset className="segmented">
            <legend className="visually-hidden">Range</legend>
            {[7, 30].map((rangeDays) => <label key={rangeDays}><input type="radio" name="calendar-range" checked={days === rangeDays} onChange={() => setDays(rangeDays)} /><span>{rangeDays} days</span></label>)}
          </fieldset>
          <label>
            Calendar
            <select value={calendarFilter} onChange={(changeEvent) => setCalendarFilter(changeEvent.target.value)}><option value="">All selected</option>{selectedCalendars.map((calendar) => <option key={calendar.id} value={calendar.id}>{calendar.name}</option>)}</select>
          </label>
          <button type="button" className="btn" onClick={() => setAnchor(today())}>Today</button>
        </div>
      )}

      {selectedCalendars.length === 0 && !managing && <EmptyState><p>Select at least one calendar to show events and grant agent access.</p><button type="button" className="btn btn-primary" onClick={() => setManaging(true)}>Choose calendars</button></EmptyState>}
      {selectedCalendars.length > 0 && events.loading && <Loading label="Loading calendar…" />}
      {events.stale && <StaleNotice message="Showing the last loaded calendar; refresh failed." onRetry={events.reload} />}
      {selectedCalendars.length > 0 && !events.loading && !events.data && <ErrorState message="Couldn't load calendar events." onRetry={events.reload} />}
      {events.data && events.data.length === 0 && <EmptyState><p>No events in this range.</p><button type="button" className="btn btn-primary" onClick={() => setEditing('new')}>Add an event</button></EmptyState>}
      {grouped.length > 0 && <div className="calendar-agenda">{grouped.map(([date, items]) => <section key={date}><h2><time dateTime={date}>{localDate(date).toLocaleDateString([], { weekday: 'long', month: 'short', day: 'numeric' })}</time></h2><ol>{items.map((event) => <EventRow key={`${event.id}:${event.start}`} event={event} onEdit={() => void openEvent(event)} />)}</ol></section>)}</div>}

      {editing && <EventEditor key={editing === 'new' ? 'new' : `${editing.id}:${editing.etag}`} event={editing === 'new' ? null : editing} calendars={selectedCalendars} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); events.reload() }} />}
      <ConfirmDialog open={confirmDisconnect} title="Disconnect Nextcloud?" confirmLabel="Disconnect" busy={disconnecting} onCancel={() => setConfirmDisconnect(false)} onConfirm={() => void disconnect()}><p>Ledger and its MCP clients will immediately lose calendar access. Existing events remain in Nextcloud.</p></ConfirmDialog>
    </>
  )
}

export function CalendarPage() {
  const connection = useResource(() => api.getCalendarConnection(), 'calendar:connection', 'calendar')
  if (connection.loading) return <Loading label="Loading calendar connection…" />
  if (!connection.data) return <ErrorState message="Couldn't load calendar connection." onRetry={connection.reload} />
  if (!connection.data.connected) return <ConnectCalendar onConnected={connection.reload} />
  return <CalendarWorkspace connection={connection.data} onDisconnected={connection.reload} />
}
