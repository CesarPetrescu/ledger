import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { api, describeError, type CalendarConnection, type EntryFilter, type TableEntry, type CalendarEvent, type CalendarEventInput, type CalendarSource } from '../api'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { EntrySplit, useEntrySelection } from '../components/EntryPanel'
import { titleOf } from '../components/entries'
import { navigate } from '../router'
import { addDays, Agenda, modeRange, modeTitle, MonthGrid, step, WeekColumns, type CalendarItem, type CalendarMode } from './calendarViews'
import { useToast } from '../components/Toast'
import { EmptyState, ErrorState, Icon, Loading, StaleNotice } from '../components/ui'
import { useResource } from '../hooks/useResource'

function dateKey(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function today(): string { return dateKey(new Date()) }

function localDate(value: string): Date {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(year!, month! - 1, day!)
}

function localDateKey(value: string): string {
  return dateKey(new Date(value))
}

function shiftDate(value: string, days: number): string {
  const date = localDate(value)
  date.setDate(date.getDate() + days)
  return dateKey(date)
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
              setDraft((current) => {
                const startDate = current.start.slice(0, 10)
                const endDate = current.end.slice(0, 10)
                if (allDay) return { ...current, allDay, start: startDate, end: endDate > startDate ? endDate : shiftDate(startDate, 1) }
                return { ...current, allDay, start: `${startDate}T09:00`, end: `${endDate > startDate ? shiftDate(endDate, -1) : startDate}T10:00` }
              })
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

function CalendarWorkspace({ connection, onDisconnected }: { connection: CalendarConnection; onDisconnected: () => void }) {
  const calendars = useResource(() => (connection.connected ? api.listCalendars() : Promise.resolve([])), `calendar:sources:${connection.connected}`, 'calendar')
  const [selection, setSelection] = useState<string[] | null>(null)
  const [managing, setManaging] = useState(connection.connected && connection.selected_calendars === 0)
  const [savingSelection, setSavingSelection] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)
  const [anchor, setAnchor] = useState(today())
  const [mode, setMode] = useState<CalendarMode>('month')
  const [calendarFilter, setCalendarFilter] = useState('')
  const [editing, setEditing] = useState<CalendarEvent | 'new' | null>(null)
  const toast = useToast()
  const entrySelection = useEntrySelection()

  const { first, days } = modeRange(mode, anchor)
  const last = addDays(first, days)
  const selectedCalendars = (calendars.data ?? []).filter((calendar) => calendar.selected)
  const activeSelection = selection ?? selectedCalendars.map((calendar) => calendar.id)
  const hasCalendars = connection.connected && selectedCalendars.length > 0
  const events = useResource(
    () => (hasCalendars ? api.listCalendarEvents(localDate(first).toISOString(), localDate(last).toISOString(), calendarFilter) : Promise.resolve([])),
    `calendar:events:${hasCalendars}:${first}:${last}:${calendarFilter}`, 'calendar')
  const ledger = useResource(() => ledgerDates(first, last), `calendar:ledger:${first}:${last}`, 'project entry entry_meta entry_owner_state')

  const items = useMemo<CalendarItem[]>(() => {
    const out: CalendarItem[] = []
    for (const event of events.data ?? []) {
      const start = event.all_day ? localDate(event.start) : new Date(event.start)
      const end = event.all_day ? localDate(event.end) : new Date(event.end)
      const time = event.all_day ? 'All day' : `${start.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}–${end.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
      // An event shows on every day it covers, so one that started earlier still shows.
      const startDay = event.all_day ? event.start : localDateKey(event.start)
      // All-day ends are exclusive; a timed event ending at midnight does not reach that day.
      const endDay = event.all_day ? event.end : dateKey(new Date(end.getTime() - 1))
      const lastDay = event.all_day ? addDays(endDay, -1) : endDay
      for (let day = startDay < first ? first : startDay; day <= lastDay && day < last; day = addDays(day, 1)) {
        out.push({ key: `e:${event.id}:${event.start}:${day}`, date: day, time: day === startDay ? time : 'Continues', sort: `${event.all_day ? '0' : '1'}${event.start}`, title: event.title || 'Untitled event',
          detail: `${event.calendar_name}${event.location ? ` · ${event.location}` : ''}${event.recurring ? ' · Recurring' : ''}`, kind: 'event', color: eventColor(event.calendar_id), open: () => void openEvent(event) })
      }
    }
    const now = today()
    for (const todo of ledger.data?.due ?? []) {
      const due = todo.meta?.due ?? ''
      out.push({ key: `t:${todo.id}`, date: due, time: 'Todo due', sort: `2${titleOf(todo)}`, title: titleOf(todo), detail: todo.project_name, kind: 'todo', overdue: due < now, entryId: todo.id, open: () => entrySelection.open(todo.id) })
    }
    for (const entry of ledger.data?.waking ?? []) {
      const day = entry.owner.snoozed_until ?? ''
      out.push({ key: `w:${entry.id}`, date: day, time: 'Wakes up', sort: `3${titleOf(entry)}`, title: titleOf(entry), detail: `${entry.project_name} · snoozed`, kind: 'wake', entryId: entry.id, open: () => entrySelection.open(entry.id) })
    }
    for (const project of ledger.data?.deadlines ?? []) {
      out.push({ key: `d:${project.slug}`, date: project.deadline, time: 'Deadline', sort: `0${project.name}`, title: project.name, detail: 'Project deadline', kind: 'deadline', open: () => navigate(`/projects/${encodeURIComponent(project.slug)}`) })
    }
    return out.filter((item) => item.date >= first && item.date < last)
  // eslint-disable-next-line react-hooks/exhaustive-deps -- openEvent only reads state at click time
  }, [events.data, ledger.data, first, last, entrySelection.selected])

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
          <p className="eyebrow">{connection.connected ? `Nextcloud · ${connection.username}` : 'Ledger dates'}</p>
          <h1>Calendar</h1>
          <p className="muted">{connection.connected ? `Your selected calendars, live from ${connection.server_url}, with` : 'Shows'} todos that are due, project deadlines, and the day snoozed items come back.</p>
        </div>
        {connection.connected && (
          <div className="calendar-head-actions">
            <button type="button" className="btn" onClick={() => setManaging((open) => !open)}>Calendars</button>
            {selectedCalendars.length > 0 && <button type="button" className="btn btn-primary" onClick={() => setEditing('new')}><Icon name="plus" /> Add event</button>}
          </div>
        )}
      </header>
      {!connection.connected && <details className="calendar-connect-later"><summary>Connect a Nextcloud calendar to see your events here too</summary><ConnectCalendar onConnected={onDisconnected} /></details>}

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

      {connection.connected && selectedCalendars.length === 0 && !managing && calendars.data && <EmptyState><p>Select at least one Nextcloud calendar to show its events here and give agents access to it.</p><button type="button" className="btn btn-primary" onClick={() => setManaging(true)}>Choose calendars</button></EmptyState>}

      <div className="calendar-toolbar">
        <div className="cal-nav">
          <button type="button" className="icon-button" aria-label="Previous" onClick={() => setAnchor(step(mode, anchor, -1))}><Icon name="back" /></button>
          <h2 className="cal-title" aria-live="polite">{modeTitle(mode, anchor)}</h2>
          <button type="button" className="icon-button cal-next" aria-label="Next" onClick={() => setAnchor(step(mode, anchor, 1))}><Icon name="back" /></button>
          <button type="button" className="btn btn-small" onClick={() => setAnchor(today())}>Today</button>
        </div>
        <fieldset className="segmented">
          <legend className="visually-hidden">View</legend>
          <div>{(['month', 'week', 'agenda'] as const).map((option) => <label key={option}><input type="radio" name="calendar-view" checked={mode === option} onChange={() => setMode(option)} /><span>{option === 'month' ? 'Month' : option === 'week' ? 'Week' : 'Agenda'}</span></label>)}</div>
        </fieldset>
        {hasCalendars && (
          <label className="cal-filter">
            <span className="visually-hidden">Calendar</span>
            <select value={calendarFilter} onChange={(changeEvent) => setCalendarFilter(changeEvent.target.value)}><option value="">All calendars</option>{selectedCalendars.map((calendar) => <option key={calendar.id} value={calendar.id}>{calendar.name}</option>)}</select>
          </label>
        )}
      </div>
      <p className="cal-legend muted small"><span data-kind="event">Events</span><span data-kind="todo">Todos due</span><span data-kind="deadline">Project deadlines</span><span data-kind="wake">Snoozed items waking</span></p>

      {connection.connected && !managing && !calendars.loading && !calendars.data && <ErrorState message="Couldn't reach your Nextcloud calendars; only Ledger's own dates are shown." onRetry={calendars.reload} />}
      {events.stale && <StaleNotice message="Showing the last loaded calendar; refresh failed." onRetry={events.reload} />}
      {hasCalendars && !events.loading && !events.data && <ErrorState message="Couldn't load calendar events." onRetry={events.reload} />}
      {!ledger.loading && !ledger.data && <ErrorState message="Couldn't load todos, deadlines, and snoozed items." onRetry={ledger.reload} />}
      {(events.loading || ledger.loading) && !events.data && !ledger.data ? <Loading label="Loading calendar…" />
        : mode === 'month' ? <MonthGrid anchor={anchor} items={items} today={today()} onDay={(day) => { setAnchor(day); setMode('agenda') }} />
        : mode === 'week' ? <WeekColumns anchor={anchor} items={items} today={today()} />
        : <Agenda items={items} today={today()} />}

      {editing && <EventEditor key={editing === 'new' ? 'new' : `${editing.id}:${editing.etag}`} event={editing === 'new' ? null : editing} calendars={selectedCalendars} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); events.reload() }} />}
      <ConfirmDialog open={confirmDisconnect} title="Disconnect Nextcloud?" confirmLabel="Disconnect" busy={disconnecting} onCancel={() => setConfirmDisconnect(false)} onConfirm={() => void disconnect()}><p>Ledger and its MCP clients will immediately lose calendar access. Existing events remain in Nextcloud.</p></ConfirmDialog>
    </>
  )
}

/** Todos due, snoozed items waking, and project deadlines between two days. */
async function ledgerDates(first: string, last: string) {
  const [due, waking, projects] = await Promise.all([
    everyEntry({ kind: 'todo', status: 'open', due_from: first, due_before: last }),
    // By wake date, so an item stays on its day once that day has come.
    everyEntry({ wakes_from: first, wakes_before: last }),
    api.listProjects(),
  ])
  return {
    due,
    waking: waking.filter((entry) => (entry.owner.snoozed_until ?? '') >= first && (entry.owner.snoozed_until ?? '') < last),
    // Only a real date counts; some deadlines are words like "daily", or impossible days like 31 September.
    deadlines: projects.filter((project) => realDate(project.deadline) && project.deadline >= first && project.deadline < last),
  }
}

/** Every page of a filtered entry list; the filters bound it to the view's days. */
async function everyEntry(filter: EntryFilter) {
  const out: TableEntry[] = []
  let before: string | undefined
  for (;;) {
    const result = await api.listEntries(filter, before)
    out.push(...result.entries)
    // A cursor that does not move would page forever.
    if (!result.next_before || result.next_before === before) return out
    before = result.next_before
  }
}

function realDate(value: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(value) && dateKey(localDate(value)) === value
}

export function CalendarPage() {
  const connection = useResource(() => api.getCalendarConnection(), 'calendar:connection', 'calendar')
  if (connection.loading) return <Loading label="Loading calendar…" />
  if (!connection.data) return <ErrorState message="Couldn't load calendar connection." onRetry={connection.reload} />
  // Ledger's own dates show even without Nextcloud; clicking a todo opens it beside the calendar.
  // Keyed by connection, so connecting or disconnecting starts from a clean page.
  return <EntrySplit><CalendarWorkspace key={String(connection.data.connected)} connection={connection.data} onDisconnected={connection.reload} /></EntrySplit>
}
