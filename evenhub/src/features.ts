/** Durable application state and pure formatting shared by native and contract tests. */
export type EntryKind = 'note' | 'todo' | 'decision' | 'status'
export interface CaptureDraft { key: string; slug: string; kind: EntryKind; body: string; confirmed: boolean; clientId?: string }
export interface EntrySource { id: string; slug: string; kind: EntryKind; body: string; source: string; created_at: string }
export interface Change extends EntrySource { cursor: string }
export interface ChangePage { entries: Change[]; checkpoint: string; next_cursor: string; through: string; has_more: boolean }
export interface SearchHit { ref: string; project_slug: string; kind: string; snippet: string }
export interface SearchResult { hits: SearchHit[]; degraded: string[] }
export interface CalendarEvent { id: string; calendar_id: string; calendar_name: string; title: string; start: string; end: string; all_day: boolean; location?: string }
export interface DurableStorage { getLocalStorage(key: string): Promise<string>; setLocalStorage(key: string, value: string): Promise<boolean> }

export class CaptureOutbox {
  private readonly key: string
  constructor(private readonly storage: DurableStorage, server: string) { this.key = `ledger-glass-capture-v1:${server}` }
  async load(): Promise<CaptureDraft | null> {
    const raw = await this.storage.getLocalStorage(this.key)
    if (!raw) return null
    let draft: CaptureDraft
    try { draft = JSON.parse(raw) }
    catch { throw new Error('Stored capture is unreadable; it has not been deleted') }
    if (!draft || !/^[a-zA-Z0-9_-]{8,80}$/.test(draft.key) || typeof draft.slug !== 'string' || !['note','todo','decision','status'].includes(draft.kind) || typeof draft.body !== 'string' || [...draft.body].length > 4000 || typeof draft.confirmed !== 'boolean' || (draft.confirmed && !draft.clientId)) throw new Error('Stored capture is invalid; it has not been deleted')
    return draft
  }
  async save(draft: CaptureDraft): Promise<void> {
    if (!await this.storage.setLocalStorage(this.key, JSON.stringify(draft))) throw new Error('Capture could not be stored on this phone; nothing was submitted')
  }
  async discard(): Promise<void> {
    if (!await this.storage.setLocalStorage(this.key, '')) throw new Error('Capture could not be cleared from phone storage')
  }
  async submit(draft: CaptureDraft, clientId: string, write: (draft: CaptureDraft) => Promise<{ id: string | number }>): Promise<{ id: string | number }> {
    if (!draft.confirmed) throw new Error('Capture requires explicit confirmation')
    if (draft.clientId !== clientId) throw new Error('Pending capture belongs to another device authorization. Check Ledger before resubmitting; automatic replay is blocked.')
    await this.save(draft) // Persist before crossing the write boundary.
    const receipt = await write(draft)
    if (!receipt || !/^[1-9][0-9]*$/.test(String(receipt.id))) throw new Error('Ledger did not acknowledge this capture; retry with the same request key')
    // Clearing may fail; retaining the identical confirmed key still makes retries safe.
    await this.discard()
    return receipt
  }
}

export function textPages(content: string, columns = 42, rows = 7): string[] {
  const lines: string[] = []
  for (const paragraph of content.replace(/\r/g, '').split('\n')) {
    if (!paragraph) { lines.push(''); continue }
    let line = ''
    for (const word of paragraph.split(/\s+/)) {
      const chars = [...word]
      if (line && [...line].length + chars.length + 1 > columns) { lines.push(line); line = '' }
      while (chars.length > columns) { lines.push(chars.splice(0, columns).join('')) }
      if (chars.length) line += `${line ? ' ' : ''}${chars.join('')}`
    }
    if (line) lines.push(line)
  }
  const pages: string[] = []
  for (let i = 0; i < lines.length; i += rows) pages.push(lines.slice(i, i + rows).join('\n'))
  return pages.length ? pages : ['']
}

export function formatCalendarEvent(event: CalendarEvent, zone: string): string {
  // DATE values must not be interpreted as UTC midnight and shifted a day.
  const start = event.all_day ? `${event.start.slice(0,10)} · All day` : new Intl.DateTimeFormat('en-GB', {
    timeZone: zone, year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).format(new Date(event.start))
  return `${event.title}\n${start}\n${event.all_day ? `Until ${event.end.slice(0,10)} (exclusive)` : zone}\n${event.calendar_name}${event.location ? `\n${event.location}` : ''}`
}

export function pcmToWav(parts: Uint8Array[]): Uint8Array {
  const length = parts.reduce((sum, part) => sum + part.length, 0)
  if (length < 3200 || length > 960000 || length % 2) throw new Error('Record between 0.1 and 30 seconds of speech')
  const bytes = new Uint8Array(44 + length)
  const view = new DataView(bytes.buffer)
  const ascii = (offset: number, s: string) => [...s].forEach((c, i) => bytes[offset+i] = c.charCodeAt(0))
  ascii(0, 'RIFF'); view.setUint32(4, length+36, true); ascii(8, 'WAVE'); ascii(12, 'fmt ')
  view.setUint32(16, 16, true); view.setUint16(20, 1, true); view.setUint16(22, 1, true)
  view.setUint32(24, 16000, true); view.setUint32(28, 32000, true); view.setUint16(32, 2, true); view.setUint16(34, 16, true)
  ascii(36, 'data'); view.setUint32(40, length, true)
  let offset = 44
  for (const part of parts) { bytes.set(part, offset); offset += part.length }
  return bytes
}
export function base64(bytes: Uint8Array): string {
  let value = ''
  for (let i = 0; i < bytes.length; i += 16384) value += String.fromCharCode(...bytes.subarray(i, i+16384))
  return btoa(value)
}
