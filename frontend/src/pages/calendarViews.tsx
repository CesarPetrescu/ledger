import type { ReactNode } from 'react'

// Month, week, and agenda layouts shared by calendar events and what Ledger
// itself knows about dates: todos due, project deadlines, snoozed items waking.

export type CalendarMode = 'month' | 'week' | 'agenda'

export interface CalendarItem {
  key: string
  /** Local day, YYYY-MM-DD. */
  date: string
  /** "All day" or a local time; items sort by this within a day. */
  time: string
  sort: string
  title: string
  detail?: string
  kind: 'event' | 'todo' | 'deadline' | 'wake'
  color?: number
  overdue?: boolean
  entryId?: string
  open: () => void
}

export function dateKey(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

export function localDate(value: string): Date {
  const [year, month, day] = value.split('-').map(Number)
  return new Date(year!, month! - 1, day!)
}

export function addDays(value: string, days: number): string {
  const date = localDate(value)
  date.setDate(date.getDate() + days)
  return dateKey(date)
}

/** Monday on or before a day. */
function weekStart(value: string): string {
  const day = localDate(value).getDay()
  return addDays(value, -((day + 6) % 7))
}

/** The days a mode shows around an anchor day: [first, last] plus how to step. */
export function modeRange(mode: CalendarMode, anchor: string): { first: string; days: number } {
  if (mode === 'week') return { first: weekStart(anchor), days: 7 }
  if (mode === 'agenda') return { first: anchor, days: 30 }
  const start = weekStart(`${anchor.slice(0, 8)}01`)
  return { first: start, days: 42 }
}

/** The anchor after moving one step back or forward. */
export function step(mode: CalendarMode, anchor: string, direction: 1 | -1): string {
  if (mode === 'week') return addDays(anchor, 7 * direction)
  if (mode === 'agenda') return addDays(anchor, 30 * direction)
  const date = localDate(`${anchor.slice(0, 8)}01`)
  date.setMonth(date.getMonth() + direction)
  return dateKey(date)
}

const monthTitle = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' })
const shortDay = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short' })
const weekday = new Intl.DateTimeFormat(undefined, { weekday: 'short' })
const longDay = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'short' })

export function modeTitle(mode: CalendarMode, anchor: string): string {
  if (mode === 'month') return monthTitle.format(localDate(anchor))
  const { first, days } = modeRange(mode, anchor)
  return `${shortDay.format(localDate(first))} – ${shortDay.format(localDate(addDays(first, days - 1)))}`
}

function byDay(items: CalendarItem[]): Map<string, CalendarItem[]> {
  const days = new Map<string, CalendarItem[]>()
  for (const item of [...items].sort((a, b) => a.sort.localeCompare(b.sort))) days.set(item.date, [...(days.get(item.date) ?? []), item])
  return days
}

function Chip({ item, compact }: { item: CalendarItem; compact?: boolean }) {
  return (
    <button type="button" className="cal-item" data-kind={item.kind} data-color={item.color} data-overdue={item.overdue ? 'true' : undefined}
      data-entry-id={item.entryId} onClick={item.open} title={[item.time, item.title, item.detail].filter(Boolean).join(' · ')}>
      {!compact && <span className="cal-item-time">{item.time}</span>}
      {compact && item.time !== 'All day' && item.kind === 'event' && <span className="cal-item-time">{item.time.split('–')[0]}</span>}
      <span className="cal-item-title">{item.title}</span>
      {!compact && item.detail && <span className="cal-item-detail">{item.detail}</span>}
    </button>
  )
}

const MONTH_LIMIT = 3

export function MonthGrid({ anchor, items, today, onDay }: { anchor: string; items: CalendarItem[]; today: string; onDay: (day: string) => void }) {
  const { first, days } = modeRange('month', anchor)
  const grouped = byDay(items)
  const month = anchor.slice(0, 7)
  const cells: ReactNode[] = []
  for (let index = 0; index < days; index++) {
    const day = addDays(first, index)
    const list = grouped.get(day) ?? []
    cells.push(
      <div key={day} className="cal-cell" role="gridcell" data-outside={day.slice(0, 7) !== month ? 'true' : undefined} data-today={day === today ? 'true' : undefined}>
        <button type="button" className="cal-day" onClick={() => onDay(day)} aria-label={`${longDay.format(localDate(day))}, ${list.length} ${list.length === 1 ? 'item' : 'items'}`}>{localDate(day).getDate()}</button>
        {list.slice(0, MONTH_LIMIT).map((item) => <Chip key={item.key} item={item} compact />)}
        {list.length > MONTH_LIMIT && <button type="button" className="cal-more" onClick={() => onDay(day)}>+{list.length - MONTH_LIMIT} more</button>}
      </div>,
    )
  }
  return (
    <div className="cal-month" role="grid" aria-label={monthTitle.format(localDate(anchor))}>
      <div className="cal-weekdays" role="row">{Array.from({ length: 7 }, (_, index) => <span key={index} role="columnheader">{weekday.format(localDate(addDays(first, index)))}</span>)}</div>
      <div className="cal-cells" role="row">{cells}</div>
    </div>
  )
}

export function WeekColumns({ anchor, items, today }: { anchor: string; items: CalendarItem[]; today: string }) {
  const { first } = modeRange('week', anchor)
  const grouped = byDay(items)
  return (
    <div className="cal-week">
      {Array.from({ length: 7 }, (_, index) => {
        const day = addDays(first, index)
        const list = grouped.get(day) ?? []
        return (
          <section key={day} className="cal-week-day" data-today={day === today ? 'true' : undefined} aria-label={longDay.format(localDate(day))}>
            <h2><span>{weekday.format(localDate(day))}</span> {localDate(day).getDate()}</h2>
            {list.length === 0 ? <p className="muted small">Nothing</p> : list.map((item) => <Chip key={item.key} item={item} />)}
          </section>
        )
      })}
    </div>
  )
}

/** Days with something on them; `overdue` (open todos due before the first day) leads, oldest first, each labeled with its due day. */
export function Agenda({ items, overdue = [], today }: { items: CalendarItem[]; overdue?: CalendarItem[]; today: string }) {
  const grouped = [...byDay(items).entries()].sort(([a], [b]) => a.localeCompare(b))
  if (grouped.length === 0 && overdue.length === 0) return <p className="muted">Nothing in these 30 days.</p>
  return (
    <div className="calendar-agenda">
      {overdue.length > 0 && (
        <section data-overdue="true">
          <h2>Overdue</h2>
          <div className="cal-agenda-items">{[...overdue].sort((a, b) => a.date.localeCompare(b.date) || a.sort.localeCompare(b.sort)).map((item) => <Chip key={item.key} item={{ ...item, time: `Due ${shortDay.format(localDate(item.date))}` }} />)}</div>
        </section>
      )}
      {grouped.length === 0 && <p className="muted">Nothing in these 30 days.</p>}
      {grouped.map(([day, list]) => (
        <section key={day} data-today={day === today ? 'true' : undefined}>
          <h2><time dateTime={day}>{longDay.format(localDate(day))}</time></h2>
          <div className="cal-agenda-items">{list.map((item) => <Chip key={item.key} item={item} />)}</div>
        </section>
      ))}
    </div>
  )
}
