import { AudioInputSource, ListContainerProperty, ListItemContainerProperty, OsEventTypeList, RebuildPageContainer, TextContainerProperty, type EvenAppBridge, type EvenHubEvent, type MenuContainerProperty } from '@evenrealities/even_hub_sdk'
import type { LedgerAuth, PairingPrompt } from './auth'
import type { LedgerMCP } from './ledger'
import { CaptureOutbox, base64, pcmToWav, textPages, formatCalendarEvent, type CaptureDraft, type ChangePage, type EntryKind, type CalendarEvent } from './features'
import { sortProjects } from './format'

export type DailyMode = 'capture' | 'recall' | 'brief' | 'next'
type Action = () => Promise<void>
interface Dependencies {
  bridge: EvenAppBridge
  auth: LedgerAuth
  ledger: LedgerMCP
  server: string
  menu: () => MenuContainerProperty
  text: (content: string) => Promise<void>
  read: <T>(operation: (token: string) => Promise<T>) => Promise<T>
  pairing: (prompt: PairingPrompt) => Promise<void>
  home: Action
  run: (action: Action) => void
  observe: (screen: string, metadata?: Record<string, unknown>) => void
  defaultProject: () => string | undefined
}

function el<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id)
  if (!element) throw new Error(`Missing #${id}`)
  return element as T
}
function label(text: string): string {
  const chars = [...text.replace(/\s+/g, ' ')]
  while (new TextEncoder().encode(chars.join('')).length > 54) chars.pop()
  return chars.join('')
}

/** Real app controller. No test-mode branches, fixture URLs, or injected results. */
export class DailyFeatures {
  active = false
  private mode: DailyMode = 'capture'
  private actions: Action[] = []
  private press: Action | undefined
  private readonly outbox: CaptureOutbox
  private draft: CaptureDraft | null = null
  private changes: ChangePage | undefined
  private query = ''
  private submitText: ((value: string) => Promise<void>) | undefined
  private recording: DailyMode | undefined
  private pcm: Uint8Array[] = []
  private pcmBytes = 0
  private timer: ReturnType<typeof setTimeout> | undefined
  private transcription: AbortController | undefined
  private generation = 0
  private readonly editor = el<HTMLFormElement>('phone-editor')
  private readonly input = el<HTMLTextAreaElement>('phone-text')
  private readonly status = el<HTMLElement>('phone-status')
  private readonly zone = el<HTMLInputElement>('time-zone')
  private readonly language = el<HTMLSelectElement>('speech-language')

  constructor(private readonly d: Dependencies) {
    this.outbox = new CaptureOutbox(d.bridge, d.server)
    this.zone.value = localStorage.getItem('ledger-glass-zone') || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
    try { new Intl.DateTimeFormat('en', { timeZone: this.zone.value }).format() } catch { this.zone.value = 'UTC' }
    this.language.value = localStorage.getItem('ledger-glass-language') || ''
    this.editor.addEventListener('submit', event => { event.preventDefault(); this.submit() })
    this.input.addEventListener('keydown', event => {
      if (event.ctrlKey && event.key === 'Enter') { event.preventDefault(); this.submit() }
    })
    el('cancel-action').addEventListener('click', () => { this.interrupt(); d.run(() => this.cancel()) })
    el('voice-action').addEventListener('click', () => d.run(() => this.recording ? this.finishRecording() : this.startRecording()))
    this.zone.addEventListener('change', () => {
      try { new Intl.DateTimeFormat('en', { timeZone: this.zone.value }).format(); localStorage.setItem('ledger-glass-zone', this.zone.value) }
      catch { this.zone.value = 'UTC'; this.status.textContent = 'Invalid time zone. Using UTC.' }
    })
    this.language.addEventListener('change', () => localStorage.setItem('ledger-glass-language', this.language.value))
    el('reload-app').addEventListener('click', () => { this.interrupt(); location.reload() })
    // Accessible phone keyboard equivalent of the visible Reload app button.
    document.addEventListener('keydown', event => {
      if (event.ctrlKey && event.altKey && event.code === 'KeyR') { event.preventDefault(); this.interrupt(); location.reload() }
    })
    document.addEventListener('visibilitychange', () => { if (document.hidden) this.interrupt() })
    window.addEventListener('pagehide', () => this.interrupt())
  }

  private submit(): void {
    const operation = this.submitText
    const value = this.input.value.trim()
    if (operation) this.d.run(() => operation(value))
  }
  interrupt(): void {
    this.generation++
    this.d.auth.cancelPairing()
    this.transcription?.abort()
    clearTimeout(this.timer)
    if (this.recording) void this.d.bridge.audioControl(false).catch(() => {})
    this.recording = undefined
    el('voice-action').textContent = 'Record speech'
    this.pcm = []; this.pcmBytes = 0
  }
  leave(): void {
    this.interrupt()
    this.active = false
    this.editor.hidden = true
    this.actions = []; this.press = undefined; this.submitText = undefined
  }
  audio(event: EvenHubEvent): void {
    if (!this.recording || !event.audioEvent) return
    const data = event.audioEvent.audioPcm
    if (!data.length) return
    if (this.pcmBytes + data.length > 960000) {
      void this.d.bridge.audioControl(false).catch(() => {})
      this.d.run(() => this.finishRecording())
      return
    }
    this.pcm.push(new Uint8Array(data)); this.pcmBytes += data.length
  }
  async handle(event: EvenHubEvent): Promise<boolean> {
    if (!this.active) return false
    if (event.listEvent && (event.listEvent.eventType ?? 0) === OsEventTypeList.CLICK_EVENT) {
      const index = event.listEvent.currentSelectItemIndex ?? 0
      if (Number.isInteger(index) && index >= 0) await this.actions[index]?.()
    } else if (event.sysEvent) {
      const type = event.sysEvent.eventType ?? OsEventTypeList.CLICK_EVENT
      if (type === OsEventTypeList.CLICK_EVENT) await this.press?.()
      else if (type === OsEventTypeList.DOUBLE_CLICK_EVENT) await this.cancel()
    }
    return true
  }
  async open(mode: DailyMode): Promise<void> {
    this.interrupt(); this.active = true; this.mode = mode
    this.editor.hidden = true; this.actions = []; this.press = undefined
    if (mode === 'capture') await this.openCapture()
    else if (mode === 'recall') await this.openRecall()
    else if (mode === 'brief') await this.openBrief()
    else await this.openNext()
  }
  async refresh(): Promise<void> {
    if (this.mode === 'recall' && this.query) await this.search(this.query)
    else await this.open(this.mode)
  }
  private async cancel(): Promise<void> {
    this.interrupt()
    if (this.mode === 'capture' && this.draft && !this.draft.confirmed) { await this.outbox.discard(); this.draft = null }
    this.leave(); await this.d.home()
  }

  private async list(screen: string, title: string, names: string[], actions: Action[], metadata: Record<string, unknown> = {}): Promise<void> {
    el('pairing').hidden = true
    this.actions = actions; this.press = undefined
    const ok = await this.d.bridge.rebuildPageContainer(new RebuildPageContainer({
      containerTotalNum: 2,
      textObject: [new TextContainerProperty({ xPosition: 0, yPosition: 0, width: 576, height: 45, containerID: 10, containerName: 'heading', isEventCapture: 0, content: title, textColor: 4, paddingLength: 8 })],
      listObject: [new ListContainerProperty({ xPosition: 0, yPosition: 48, width: 576, height: 240, containerID: 11, containerName: 'actions', isEventCapture: 1, paddingLength: 8,
        itemContainer: new ListItemContainerProperty({ itemCount: names.length, itemWidth: 0, isItemSelectBorderEn: 1, itemName: names.map(label) }),
      })], menuObject: this.d.menu(),
    }))
    if (!ok) throw new Error('Even Hub rejected the action list')
    this.status.textContent = title
    this.d.observe(screen, metadata)
  }
  private async document(screen: string, title: string, body: string, done: Action, metadata: Record<string, unknown> = {}): Promise<void> {
    el('pairing').hidden = true
    const pages = textPages(body)
    const render = async (index: number): Promise<void> => {
      await this.d.text(`${title}\n\n${pages[index]}\n\n${index+1}/${pages.length} · Press: ${index+1 === pages.length ? 'actions' : 'next page'}`)
      this.actions = []
      this.press = index+1 < pages.length ? () => render(index+1) : done
      this.d.observe(screen, { ...metadata, page: index, pages: pages.length })
    }
    await render(0)
  }
  private edit(title: string, text: string, submit: (text: string) => Promise<void>): void {
    this.editor.hidden = false; this.input.value = text
    el('editor-label').textContent = title
    this.submitText = submit
    this.input.focus()
  }

  private async openCapture(): Promise<void> {
    this.draft = await this.outbox.load()
    if (this.draft?.confirmed) { await this.pending(); return }
    if (!this.draft) {
      const projects = sortProjects((await this.d.read(token => this.d.ledger.listProjects(token))).projects)
      const project = projects.find(p => p.slug === this.d.defaultProject()) ?? projects[0]
      if (!project) { await this.document('capture-empty', 'CAPTURE', 'Create a project in Ledger first.', () => this.cancel()); return }
      this.draft = { key: crypto.randomUUID(), slug: project.slug, kind: 'note', body: '', confirmed: false }
    }
    this.edit('Capture text — review before saving', this.draft.body, value => this.reviewCapture(value))
    await this.list('capture-input', `CAPTURE · ${this.draft.slug}`, ['Record speech', 'Use phone keyboard', 'Review draft', 'Cancel'], [
      () => this.startRecording(), async () => { this.input.focus() }, () => this.reviewCapture(this.input.value), () => this.cancel(),
    ])
  }
  private async reviewCapture(body: string): Promise<void> {
    if (!this.draft || this.draft.confirmed) throw new Error('A confirmed pending capture cannot be edited')
    body = body.trim()
    if (!body || [...body].length > 4000) throw new Error('Capture must contain 1 to 4000 characters')
    this.draft.body = body
    await this.outbox.save(this.draft)
    this.input.value = body
    await this.document('capture-review', `REVIEW · ${this.draft.kind.toUpperCase()}`, `${this.draft.slug}\n${body}`, () => this.captureActions(), { slug: this.draft.slug, kind: this.draft.kind })
  }
  private async captureActions(): Promise<void> {
    await this.list('capture-actions', 'CAPTURE · Not saved', ['Confirm and save', 'Change project', 'Change type', 'Edit on phone', 'Cancel'], [
      () => this.saveCapture(), () => this.chooseProject(), () => this.chooseKind(), () => this.openCapture(), () => this.cancel(),
    ])
  }
  private async chooseProject(page = 0): Promise<void> {
    const projects = sortProjects((await this.d.read(token => this.d.ledger.listProjects(token))).projects)
    const group = projects.slice(page*17, (page+1)*17)
    const names = group.map(project => project.name)
    const actions: Action[] = group.map(project => async () => { if (this.draft) { this.draft.slug = project.slug; await this.outbox.save(this.draft); await this.reviewCapture(this.draft.body) } })
    if ((page+1)*17 < projects.length) { names.push('Next page'); actions.push(() => this.chooseProject(page+1)) }
    names.push('Back'); actions.push(() => this.captureActions())
    await this.list('capture-project', 'CHOOSE PROJECT', names, actions)
  }
  private async chooseKind(): Promise<void> {
    const kinds: EntryKind[] = ['note', 'todo', 'decision', 'status']
    await this.list('capture-kind', 'ENTRY TYPE', kinds, kinds.map(kind => async () => {
      if (this.draft) { this.draft.kind = kind; await this.outbox.save(this.draft); await this.reviewCapture(this.draft.body) }
    }))
  }
  private async saveCapture(): Promise<void> {
    if (!this.draft) throw new Error('No capture to save')
    const draft = this.draft
    this.editor.hidden = true
    try {
      await this.d.auth.requireScopes(['ledger:write'], this.d.pairing)
      const clientId = await this.d.auth.clientId()
      if (!draft.confirmed) { draft.confirmed = true; draft.clientId = clientId }
      const receipt = await this.outbox.submit(draft, clientId, pending => this.d.read(token => this.d.ledger.append(token, pending)))
      this.draft = null
      await this.document('capture-saved', 'SAVED TO LEDGER', `${draft.slug} · ${draft.kind}\nEntry #${receipt.id}`, () => this.cancel(), { id: String(receipt.id), slug: draft.slug })
    } catch (error) {
      if (draft.confirmed) await this.pending(error)
      else {
        await this.document('capture-not-saved', 'NOT SAVED', this.message(error), () => this.captureActions())
      }
    }
  }
  private async pending(error?: unknown): Promise<void> {
    this.editor.hidden = true
    if (error) this.status.textContent = this.message(error)
    await this.list('capture-pending', 'PENDING · Not acknowledged', ['Retry same capture', 'Back (keep pending)'], [() => this.saveCapture(), async () => { this.leave(); await this.d.home() }], { slug: this.draft?.slug })
    if (error) this.status.textContent = this.message(error)
  }

  private async openRecall(): Promise<void> {
    this.edit('Recall query', this.query, value => this.search(value))
    await this.list('recall-input', 'RECALL · Search Ledger', ['Record question', 'Use phone keyboard', 'Search current query', 'Back'], [
      () => this.startRecording(), async () => { this.input.focus() }, () => this.search(this.input.value), () => this.cancel(),
    ])
  }
  private async search(query: string): Promise<void> {
    query = query.trim()
    if (!query || [...query].length > 500) throw new Error('Search query must contain 1 to 500 characters')
    this.query = query
    this.editor.hidden = true
    const result = await this.d.read(token => this.d.ledger.search(token, query))
    if (!result.hits.length) {
      await this.document('recall-empty', 'NO MATCHES', 'No matching Ledger entries. Try a different query.', () => this.openRecall(), { degraded: result.degraded }); return
    }
    const names = result.hits.map(hit => `${hit.project_slug} · ${hit.snippet}`)
    const actions = result.hits.map(hit => async () => {
      if (/^entry:[1-9][0-9]*$/.test(hit.ref)) {
        const source = await this.d.read(token => this.d.ledger.entry(token, hit.ref.slice(6)))
        await this.document('recall-source', `${source.slug} · ${source.kind}`, `${source.body}\n\n${hit.ref}\n${source.created_at}\nSource: ${source.source}`, () => this.search(this.query), { ref: hit.ref, id: source.id, created_at: source.created_at })
      } else {
        await this.document('recall-source', hit.project_slug, `${hit.snippet}\n\nSource: ${hit.ref}`, () => this.search(this.query), { ref: hit.ref })
      }
    })
    names.push('New query'); actions.push(() => this.openRecall())
    await this.list('recall-results', result.degraded.length ? 'RECALL · Lexical fallback' : 'RECALL · Sources', names, actions, { refs: result.hits.map(hit => hit.ref), degraded: result.degraded })
  }

  private async openBrief(after?: string, through?: string): Promise<void> {
    this.changes = await this.d.read(token => this.d.ledger.changes(token, after, through))
    await this.renderBrief()
  }
  private async renderBrief(): Promise<void> {
    const page = this.changes
    if (!page) throw new Error('No changes page')
    if (!page.entries.length) {
      await this.document('brief-empty', 'BRIEF · Caught up', 'No unread entries for this device. Other readers and digests are unchanged.', () => this.cancel(), { checkpoint: page.checkpoint, through: page.through }); return
    }
    const names = page.entries.map(entry => `${entry.slug} · ${entry.kind}: ${entry.body}`)
    const actions: Action[] = page.entries.map(entry => () => this.document('brief-entry', `${entry.slug} · ${entry.kind}`, `${entry.body}\n\nentry:${entry.id}\n${entry.created_at}`, () => this.renderBrief(), { id: entry.id }))
    names.push(page.has_more ? 'Mark page read / Next' : 'Mark page read / Done')
    actions.push(async () => {
      const receipt = await this.d.read(token => this.d.ledger.acknowledge(token, page.next_cursor))
      this.d.observe('brief-acknowledged', { checkpoint: receipt.checkpoint })
      if (page.has_more) await this.openBrief(page.next_cursor, page.through)
      else await this.document('brief-done', 'BRIEF · Page acknowledged', 'This snapshot is complete. Open Brief again to check for newer entries.', () => this.cancel(), { checkpoint: receipt.checkpoint })
    })
    names.push('Back (keep unread)'); actions.push(() => this.cancel())
    await this.list('brief', 'BRIEF · Unread entries', names, actions, { ids: page.entries.map(entry => entry.id), cursors: page.entries.map(entry => entry.cursor), checkpoint: page.checkpoint, through: page.through, next_cursor: page.next_cursor, has_more: page.has_more })
  }

  private async openNext(): Promise<void> {
    this.editor.hidden = true
    await this.d.auth.requireScopes(['calendar:read'], this.d.pairing)
    const start = new Date()
    const end = new Date(start.getTime() + 7*86400000)
    const events = (await this.d.read(token => this.d.ledger.events(token, start.toISOString(), end.toISOString()))).events
    const localKey = (event: CalendarEvent): string => {
      if (event.all_day) return `${event.start.slice(0,10)}T00:00:00`
      const parts = new Intl.DateTimeFormat('sv-SE', { timeZone: this.zone.value, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(event.start))
      const field = (type: string) => parts.find(part => part.type === type)?.value ?? ''
      return `${field('year')}-${field('month')}-${field('day')}T${field('hour')}:${field('minute')}:${field('second')}`
    }
    events.sort((a,b) => localKey(a).localeCompare(localKey(b)) || a.title.localeCompare(b.title))
    const focus = sortProjects((await this.d.read(token => this.d.ledger.listProjects(token))).projects)[0]
    if (!events.length) { await this.document('calendar-empty', 'NEXT · 7 days', `${focus ? `Focus: ${focus.name}\n\n` : ''}No events in the selected calendars.`, () => this.cancel()); return }
    await this.calendarPage(events, 0, focus?.name)
  }
  private async calendarPage(events: CalendarEvent[], page: number, focus?: string): Promise<void> {
    const group = events.slice(page*17, (page+1)*17)
    const names = group.map(event => `${event.all_day ? 'All day' : new Intl.DateTimeFormat('en-GB', { timeZone: this.zone.value, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(event.start))} · ${event.title}`)
    const actions: Action[] = group.map(event => () => this.document('calendar-event', 'NEXT · Selected calendar', formatCalendarEvent(event, this.zone.value), () => this.calendarPage(events,page,focus), { id: event.id, start: event.start, all_day: event.all_day, zone: this.zone.value }))
    if ((page+1)*17 < events.length) { names.push('More events'); actions.push(() => this.calendarPage(events,page+1,focus)) }
    names.push('Back to Now'); actions.push(() => this.cancel())
    await this.list('calendar', focus ? `NEXT · Focus: ${label(focus)}` : 'NEXT · 7 days', names, actions, { ids: group.map(event => event.id), calendars: group.map(event => event.calendar_id), zone: this.zone.value })
  }

  private async startRecording(): Promise<void> {
    if (!this.active || !['capture','recall'].includes(this.mode)) return
    if (this.draft?.confirmed && this.mode === 'capture') throw new Error('Resolve the pending capture first')
    this.pcm = []; this.pcmBytes = 0; this.recording = this.mode
    const ok = await this.d.bridge.audioControl(true, AudioInputSource.Glasses)
    if (!ok) { this.recording = undefined; throw new Error('Microphone is unavailable. Type on the phone instead.') }
    el('voice-action').textContent = 'Stop recording'
    await this.d.text('LISTENING\n\nPress to stop and transcribe.\n30-second maximum.\n\nDouble press cancels.\nNothing is saved automatically.')
    this.actions = []; this.press = () => this.finishRecording()
    this.d.observe('recording', { mode: this.mode })
    this.timer = setTimeout(() => { void this.d.bridge.audioControl(false).catch(() => {}); this.d.run(() => this.finishRecording()) }, 30_000)
  }
  private async finishRecording(): Promise<void> {
    if (!this.recording) return
    const mode = this.recording
    const generation = this.generation
    this.recording = undefined; clearTimeout(this.timer)
    await this.d.bridge.audioControl(false)
    el('voice-action').textContent = 'Record speech'
    const pcm = this.pcm; this.pcm = []; this.pcmBytes = 0
    const wav = pcmToWav(pcm)
    await this.d.text('TRANSCRIBING\n\nNothing is saved yet.\nCancel on the phone to discard.')
    this.d.observe('transcribing', { bytes: wav.length, mode })
    const controller = new AbortController(); this.transcription = controller
    try {
      const result = await this.d.read(token => this.d.ledger.transcribe(token, base64(wav), this.language.value, controller.signal))
      if (generation !== this.generation) return
      if (mode === 'capture') await this.reviewCapture(result.text)
      else await this.search(result.text)
    } finally { if (this.transcription === controller) this.transcription = undefined }
  }
  private message(error: unknown): string { return error instanceof Error ? error.message : String(error) }
}
