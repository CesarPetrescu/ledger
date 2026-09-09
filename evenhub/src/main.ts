import './style.css'
import {
  CreateStartUpPageContainer,
  ListContainerProperty,
  ListItemContainerProperty,
  MenuContainerProperty,
  MenuItemProperty,
  RebuildPageContainer,
  TextContainerProperty,
  waitForEvenAppBridge,
} from '@evenrealities/even_hub_sdk'
import { LedgerAuth, type PairingPrompt } from './auth'
import { normalizeServer } from './config'
import { chooseNowProject, formatError, formatNow, formatProject, projectListLabel, sortProjects } from './format'
import { LedgerMCP } from './ledger'
import type { ProjectSummary } from './types'

const MENU_NOW = 1
const MENU_PROJECTS = 2
const MENU_REFRESH = 3
const MENU_RECONNECT = 4
const MAIN_ID = 1
const MAIN_NAME = 'main'
const LIST_ID = 2
const LIST_NAME = 'projects'

const server = normalizeServer(import.meta.env.VITE_LEDGER_SERVER)
const bridge = await waitForEvenAppBridge()
const auth = new LedgerAuth(server, bridge)
const ledger = new LedgerMCP(server)
const phone = phoneUI(server)

let projects: ProjectSummary[] = []
let currentScreen: 'now' | 'projects' | 'project' | 'pairing' | 'error' = 'now'
let started = false
let queue = Promise.resolve()

await createInitialPage('LEDGER GLASS\n\nConnecting to Ledger…')
started = true
registerInput()
phone.reconnect.addEventListener('click', () => enqueue(reconnect))
await enqueue(showNow)

function registerInput(): void {
  bridge.onEvenHubEvent(event => {
    void enqueue(async () => {
      const menuID = event.menuItemClickEvent?.itemID
      if (menuID !== undefined) {
        if (menuID === MENU_NOW) await showNow()
        else if (menuID === MENU_PROJECTS) await showProjects()
        else if (menuID === MENU_REFRESH) await refreshCurrent()
        else if (menuID === MENU_RECONNECT) await reconnect()
        return
      }

      if (event.listEvent) {
        const index = event.listEvent.currentSelectItemIndex ?? 0
        if (currentScreen === 'projects' && projects[index]) await showProject(projects[index])
        return
      }

      // Contextual-menu open/close also emits foreground lifecycle events.
      // Do not turn those lifecycle signals into navigation.
      const eventType = event.sysEvent?.eventType ?? 0
      if (event.sysEvent && eventType === 3) {
        await bridge.shutDownPageContainer(1)
        return
      }
      if (event.sysEvent && (eventType === 6 || eventType === 7)) {
        await ledger.close()
      }
    })
  })
}

async function showNow(): Promise<void> {
  currentScreen = 'now'
  phone.setStatus('Loading current Ledger focus…')
  try {
    const accessToken = await token()
    const listed = await withRefresh(nextToken => ledger.listProjects(nextToken), accessToken)
    projects = sortProjects(listed.projects)
    const selected = chooseNowProject(projects)
    if (!selected) {
      await showText('LEDGER · NOW\n\nNo projects are registered.')
      phone.setStatus('Connected. No projects are registered.')
      return
    }
    const detail = await withRefresh(nextToken => ledger.getProject(nextToken, selected.slug, 5), accessToken)
    await showText(formatNow(detail))
    phone.setStatus(`Connected. Showing ${selected.name}.`)
    phone.hidePairing()
  } catch (error) {
    await fail(error)
  }
}

async function showProjects(): Promise<void> {
  currentScreen = 'projects'
  phone.setStatus('Loading projects…')
  try {
    const accessToken = await token()
    const listed = await withRefresh(nextToken => ledger.listProjects(nextToken), accessToken)
    projects = sortProjects(listed.projects).slice(0, 20)
    if (projects.length === 0) {
      await showText('PROJECTS\n\nNo projects are registered.')
      return
    }
    const ok = await bridge.rebuildPageContainer(
      new RebuildPageContainer({
        containerTotalNum: 1,
        listObject: [
          new ListContainerProperty({
            xPosition: 0,
            yPosition: 0,
            width: 576,
            height: 288,
            borderWidth: 0,
            borderColor: 5,
            borderRadius: 0,
            paddingLength: 8,
            containerID: LIST_ID,
            containerName: LIST_NAME,
            isEventCapture: 1,
            itemContainer: new ListItemContainerProperty({
              itemCount: projects.length,
              itemWidth: 0,
              isItemSelectBorderEn: 1,
              itemName: projects.map(projectListLabel),
            }),
          }),
        ],
        menuObject: menu(),
      }),
    )
    if (!ok) throw new Error('Even Hub rejected the project list layout')
    phone.setStatus(`Connected. ${projects.length} projects loaded.`)
    phone.hidePairing()
  } catch (error) {
    await fail(error)
  }
}

async function showProject(project: ProjectSummary): Promise<void> {
  currentScreen = 'project'
  phone.setStatus(`Loading ${project.name}…`)
  try {
    const accessToken = await token()
    const detail = await withRefresh(nextToken => ledger.getProject(nextToken, project.slug, 8), accessToken)
    await showText(formatProject(detail))
    phone.setStatus(`Connected. Showing ${project.name}.`)
  } catch (error) {
    await fail(error)
  }
}

async function refreshCurrent(): Promise<void> {
  if (currentScreen === 'projects') await showProjects()
  else await showNow()
}

async function reconnect(): Promise<void> {
  currentScreen = 'pairing'
  phone.setStatus('Starting a new Ledger device approval…')
  phone.hidePairing()
  await ledger.close()
  try {
    await auth.reconnect(showPairing)
    await showNow()
  } catch (error) {
    await fail(error)
  }
}

async function token(): Promise<string> {
  return auth.accessToken(showPairing)
}

async function withRefresh<T>(operation: (token: string) => Promise<T>, accessToken: string): Promise<T> {
  try {
    return await operation(accessToken)
  } catch {
    await ledger.close()
    const refreshed = await auth.refreshNow(showPairing)
    return operation(refreshed)
  }
}

async function showPairing(prompt: PairingPrompt): Promise<void> {
  currentScreen = 'pairing'
  phone.showPairing(prompt)
  phone.setStatus('Waiting for Ledger device approval…')
  await showText(`LEDGER GLASS\n\nApprove device:\n${prompt.userCode}\n\n${shortHost(prompt.verificationUri)}\n\nRead-only access`)
}

async function fail(error: unknown): Promise<void> {
  currentScreen = 'error'
  phone.setStatus(error instanceof Error ? error.message : String(error))
  await showText(formatError(error))
}

async function createInitialPage(content: string): Promise<void> {
  const result = await bridge.createStartUpPageContainer(
    new CreateStartUpPageContainer({
      containerTotalNum: 1,
      textObject: [textContainer(content)],
      menuObject: menu(),
    }),
  )
  if (result !== 0) throw new Error(`Even Hub startup page failed with code ${result}`)
}

async function showText(content: string): Promise<void> {
  if (!started) return createInitialPage(content)
  const ok = await bridge.rebuildPageContainer(
    new RebuildPageContainer({
      containerTotalNum: 1,
      textObject: [textContainer(content)],
      menuObject: menu(),
    }),
  )
  if (!ok) throw new Error('Even Hub rejected the page layout')
}

function textContainer(content: string): TextContainerProperty {
  return new TextContainerProperty({
    xPosition: 0,
    yPosition: 0,
    width: 576,
    height: 288,
    borderWidth: 0,
    borderColor: 5,
    borderRadius: 0,
    paddingLength: 10,
    containerID: MAIN_ID,
    containerName: MAIN_NAME,
    isEventCapture: 1,
    textColor: 4,
    content,
  })
}

function menu(): MenuContainerProperty {
  return new MenuContainerProperty({
    menuItems: [
      new MenuItemProperty({ itemName: 'Now', itemID: MENU_NOW }),
      new MenuItemProperty({ itemName: 'Projects', itemID: MENU_PROJECTS }),
      new MenuItemProperty({ itemName: 'Refresh', itemID: MENU_REFRESH }),
      new MenuItemProperty({ itemName: 'Reconnect', itemID: MENU_RECONNECT }),
    ],
  })
}

function enqueue<T>(task: () => Promise<T>): Promise<T> {
  const next = queue.then(task, task)
  queue = next.then(() => undefined, () => undefined)
  return next
}

function shortHost(value: string): string {
  try {
    const url = new URL(value)
    return `${url.host}${url.pathname}`
  } catch {
    return value
  }
}

function phoneUI(configuredServer: string) {
  const status = element('phone-status')
  const serverValue = element('server-value')
  const pairing = element('pairing') as HTMLElement
  const pairingCode = element('pairing-code')
  const pairingURI = element('pairing-uri')
  const reconnect = element('reconnect') as HTMLButtonElement
  serverValue.textContent = configuredServer

  return {
    reconnect,
    setStatus(value: string) { status.textContent = value },
    showPairing(prompt: PairingPrompt) {
      pairing.hidden = false
      pairingCode.textContent = prompt.userCode
      pairingURI.textContent = prompt.verificationUri
    },
    hidePairing() { pairing.hidden = true },
  }
}

function element(id: string): HTMLElement {
  const found = document.getElementById(id)
  if (!found) throw new Error(`Missing #${id}`)
  return found
}
