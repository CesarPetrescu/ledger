import './style.css'
import {
  CreateStartUpPageContainer, ListContainerProperty, ListItemContainerProperty,
  MenuContainerProperty, MenuItemProperty, OsEventTypeList, RebuildPageContainer,
  TextContainerProperty, waitForEvenAppBridge,
} from '@evenrealities/even_hub_sdk'
import { DailyFeatures, type DailyMode } from './daily'
import { LedgerAuth, type PairingPrompt } from './auth'
import { normalizeServer } from './config'
import { chooseNowProject, formatError, formatNow, formatProject, projectListLabel, sortProjects } from './format'
import { LedgerMCP } from './ledger'
import type { ProjectSummary } from './types'

const PAGE_SIZE = 18 // Leave room for both previous/next in the 20-item native list.
const server = normalizeServer(import.meta.env.VITE_LEDGER_SERVER)
const bridge = await waitForEvenAppBridge()
const auth = new LedgerAuth(server, bridge)
const ledger = new LedgerMCP(server)
const phone = phoneUI(server)
let projects: ProjectSummary[] = []
let rows: Array<ProjectSummary | 'previous' | 'next'> = []
let page = 0
let selected: ProjectSummary | undefined
let screen: 'now' | 'projects' | 'project' | 'pairing' | 'error' = 'now'
let queue = Promise.resolve()
let stopped = false

const daily = new DailyFeatures({ bridge, auth, ledger, server, menu, text: showText, read, pairing: showPairing,
  home: showNow, run: task => { void enqueue(task).catch(fail) }, observe: observed, defaultProject: () => selected?.slug,
})

await createInitialPage('LEDGER GLASS\n\nConnecting to Ledger…')
const unsubscribe = bridge.onEvenHubEvent(event => {
  if (stopped) return
  if (event.sysEvent?.eventType === OsEventTypeList.DOUBLE_CLICK_EVENT && (daily.active || screen === 'pairing')) daily.interrupt()
  daily.audio(event)
  if (event.audioEvent) return
  if (event.sysEvent?.eventType === OsEventTypeList.ABNORMAL_EXIT_EVENT || event.sysEvent?.eventType === OsEventTypeList.SYSTEM_EXIT_EVENT) daily.leave()
  void enqueue(async () => {
    const menuID = event.menuItemClickEvent?.itemID
    if (menuID !== undefined) {
      if (menuID >= 5 && menuID <= 8) { await daily.open((['capture','recall','brief','next'] as DailyMode[])[menuID-5]); return }
      if (menuID === 3 && daily.active) { await daily.refresh(); return }
      daily.leave()
      if (menuID === 1) await showNow()
      else if (menuID === 2) { page = 0; await showProjects() }
      else if (menuID === 3) await refreshCurrent()
      else if (menuID === 4) await reconnect()
      return
    }
    if (await daily.handle(event)) return
    if (event.listEvent) {
      if ((event.listEvent.eventType ?? OsEventTypeList.CLICK_EVENT) !== OsEventTypeList.CLICK_EVENT) return
      const index = event.listEvent.currentSelectItemIndex ?? 0
      if (screen !== 'projects' || !Number.isInteger(index) || index < 0) return
      const row = rows[index]
      if (row === 'next') { page++; await showProjects() }
      else if (row === 'previous') { page--; await showProjects() }
      else if (row) await showProject(row)
      return
    }
    if (!event.sysEvent) return
    const type = event.sysEvent.eventType ?? OsEventTypeList.CLICK_EVENT
    if (type === OsEventTypeList.CLICK_EVENT) {
      if (screen === 'now' || screen === 'project') await showProjects()
    } else if (type === OsEventTypeList.DOUBLE_CLICK_EVENT) {
      // Cancel keeps the subscription and the current screen alive.
      await bridge.shutDownPageContainer(1)
    } else if (type === OsEventTypeList.ABNORMAL_EXIT_EVENT || type === OsEventTypeList.SYSTEM_EXIT_EVENT) {
      stopped = true
      unsubscribe()
      await ledger.close()
      observed('exit')
    }
    // Menu foreground transitions must not navigate or rebuild under the overlay.
  }).catch(fail)
})
phone.reconnect.addEventListener('click', () => { void enqueue(reconnect).catch(reportFailure) })
await enqueue(showNow)

async function read<T>(operation: (token: string) => Promise<T>): Promise<T> {
  try {
    return await operation(await auth.accessToken(showPairing))
  } catch (error) {
    const status = error as { code?: unknown; status?: unknown }
    // Offline/502/tool errors are not evidence of an expired OAuth grant.
    if (status?.code !== 401 && status?.status !== 401) throw error
    await ledger.close()
    return operation(await auth.refreshNow(showPairing))
  }
}

async function showNow(): Promise<void> {
  try {
    phone.setStatus('Loading current Ledger focus…')
    projects = sortProjects((await read(token => ledger.listProjects(token))).projects)
    const project = chooseNowProject(projects)
    if (!project) {
      await showText('LEDGER · NOW\n\nNo projects are registered.')
      screen = 'now'
      phone.hidePairing()
      phone.setStatus('Connected. No projects are registered.')
      observed(screen, { empty: true })
      return
    }
    const detail = await read(token => ledger.getProject(token, project.slug, 5))
    await showText(formatNow(detail))
    // Pairing changes screen; commit the destination only after its render succeeds.
    screen = 'now'
    phone.hidePairing()
    phone.setStatus(`Connected. Showing ${project.name}.`)
    observed(screen, { slug: project.slug, entries: detail.entries.map(entry => entry.id) })
  } catch (error) { await fail(error) }
}

async function showProjects(): Promise<void> {
  try {
    phone.setStatus('Loading projects…')
    projects = sortProjects((await read(token => ledger.listProjects(token))).projects)
    page = Math.max(0, Math.min(page, Math.ceil(projects.length / PAGE_SIZE) - 1))
    if (projects.length === 0) {
      await showText('PROJECTS\n\nNo projects are registered.')
      rows = []
    } else {
      rows = projects.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE)
      if (page > 0) rows.unshift('previous')
      if ((page + 1) * PAGE_SIZE < projects.length) rows.push('next')
      const labels = rows.map(row => row === 'next' ? 'Next page >' : row === 'previous' ? '< Previous page' : projectListLabel(row))
      const ok = await bridge.rebuildPageContainer(new RebuildPageContainer({
        containerTotalNum: 1,
        listObject: [new ListContainerProperty({
          xPosition: 0, yPosition: 0, width: 576, height: 288,
          borderWidth: 0, borderColor: 5, borderRadius: 0, paddingLength: 8,
          containerID: 2, containerName: 'projects', isEventCapture: 1,
          itemContainer: new ListItemContainerProperty({ itemCount: labels.length, itemWidth: 0, isItemSelectBorderEn: 1, itemName: labels }),
        })],
        menuObject: menu(),
      }))
      if (!ok) throw new Error('Even Hub rejected the project list layout')
    }
    screen = 'projects'
    phone.hidePairing()
    phone.setStatus(`Connected. ${projects.length} projects loaded. Page ${page + 1}.`)
    observed(screen, { page, total: projects.length, slugs: rows.map(row => typeof row === 'string' ? row : row.slug) })
  } catch (error) { await fail(error) }
}

async function showProject(project: ProjectSummary): Promise<void> {
  try {
    phone.setStatus(`Loading ${project.name}…`)
    const detail = await read(token => ledger.getProject(token, project.slug, 8))
    await showText(formatProject(detail))
    selected = project
    screen = 'project'
    phone.hidePairing()
    phone.setStatus(`Connected. Showing ${project.name}.`)
    observed(screen, { slug: project.slug, entries: detail.entries.map(entry => entry.id) })
  } catch (error) { await fail(error) }
}

async function refreshCurrent(): Promise<void> {
  if (screen === 'projects') await showProjects()
  else if (screen === 'project' && selected) await showProject(selected)
  else await showNow()
}
async function reconnect(): Promise<void> {
  phone.hidePairing()
  await ledger.close()
  try { await auth.reconnect(showPairing); await showNow() }
  catch (error) { await fail(error) }
}
async function showPairing(prompt: PairingPrompt): Promise<void> {
  phone.showPairing(prompt)
  await showText(`LEDGER GLASS\n\nApprove device:\n${prompt.userCode}\n\n${shortHost(prompt.verificationUri)}\n\n${(prompt.scopes ?? ['ledger:read']).join(' + ')}`)
  screen = 'pairing'
  phone.setStatus('Waiting for Ledger device approval…')
  observed(screen)
}
async function fail(error: unknown): Promise<void> {
  daily.leave()
  screen = 'error'
  phone.setStatus(error instanceof Error ? error.message : String(error))
  await showText(formatError(error))
  observed(screen)
}
function reportFailure(): void {
  console.error('[ledger-glass] input/render operation failed')
}
async function createInitialPage(content: string): Promise<void> {
  const result = await bridge.createStartUpPageContainer(new CreateStartUpPageContainer({
    containerTotalNum: 1, textObject: [textContainer(content)], menuObject: menu(),
  }))
  if (result !== 0) throw new Error(`Even Hub startup page failed with code ${result}`)
  observed('ready')
}
async function showText(content: string): Promise<void> {
  const ok = await bridge.rebuildPageContainer(new RebuildPageContainer({
    containerTotalNum: 1, textObject: [textContainer(content)], menuObject: menu(),
  }))
  if (!ok) throw new Error('Even Hub rejected the page layout')
}
function textContainer(content: string): TextContainerProperty {
  return new TextContainerProperty({
    xPosition: 0, yPosition: 0, width: 576, height: 288,
    borderWidth: 0, borderColor: 5, borderRadius: 0, paddingLength: 10,
    containerID: 1, containerName: 'main', isEventCapture: 1, textColor: 4, content,
  })
}
function menu(): MenuContainerProperty {
  return new MenuContainerProperty({ menuItems: ['Now', 'Projects', 'Refresh', 'Reconnect', 'Capture', 'Recall', 'Brief', 'Next'].map((itemName, index) => new MenuItemProperty({ itemName, itemID: index + 1 })) })
}
function enqueue<T>(task: () => Promise<T>): Promise<T> {
  const next = queue.then(task, task)
  queue = next.then(() => undefined, () => undefined)
  return next
}
// Observation only, after bridge acknowledgement; never logs bodies, codes or credentials.
// The system suite uses this with real framebuffer assertions, not instead of rendering.
function observed(view: string, metadata: Record<string, unknown> = {}): void {
  console.info(`[ledger-glass:view] ${JSON.stringify({ screen: view, ...metadata })}`)
}
function shortHost(value: string): string {
  try { const url = new URL(value); return `${url.host}${url.pathname}` }
  catch { return value }
}
function phoneUI(configuredServer: string) {
  const status = element('phone-status')
  const pairing = element('pairing')
  const pairingCode = element('pairing-code')
  const pairingURI = element('pairing-uri')
  const reconnect = element('reconnect') as HTMLButtonElement
  element('server-value').textContent = configuredServer
  return {
    reconnect,
    setStatus(value: string) { status.textContent = value },
    showPairing(prompt: PairingPrompt) { pairing.hidden = false; pairingCode.textContent = prompt.userCode; pairingURI.textContent = prompt.verificationUri },
    hidePairing() { pairing.hidden = true },
  }
}
function element(id: string): HTMLElement {
  const found = document.getElementById(id)
  if (!found) throw new Error(`Missing #${id}`)
  return found
}
