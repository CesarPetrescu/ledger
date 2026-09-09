import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdir, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { chromium } from 'playwright'
import { PNG } from 'pngjs'
import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client'

const root = process.cwd()
const out = process.env.GLASS_ARTIFACT_DIR
const runtime = process.env.GLASS_RUNTIME_DIR
const server = 'https://localhost:8443'
const target = process.env.GLASS_TARGET === 'package' ? 'https://localhost:9443/ledger-glass.ehpk' : 'https://localhost:9443/'
const control = 'http://127.0.0.1:9898'
assert(out && runtime && process.env.GLASS_OWNER_PASSWORD, 'Run through system/run.sh, not against production')
const dc = ['compose', '-f', 'docker-compose.yml', '-f', 'evenhub/system/compose.yml']
const planned = ['security.tls-cors', 'owner.login', 'pairing.approve-empty', 'now.real-data', 'navigation.first-and-second', 'navigation.pagination', 'refresh.real-entry', 'menu.roundtrip', 'session.cold-restart', 'scope.write-denied', 'network.outage-recovery', 'session.revocation', 'pairing.deny', 'exit.dialog']
const fullAppRequired = ['capture.confirm', 'capture.cancel', 'capture.retry-idempotency', 'recall.sources', 'recall.degraded', 'brief.all-pages', 'brief.checkpoint-restart', 'calendar.selected', 'calendar.timezones']
const results = []
const shots = []
let sim, simOutput, browser, context, page, csrf = '', boot = 0, lastId = 0
let failed = false
const observations = []

async function until(fn, label, timeout = 45000) {
  const end = Date.now() + timeout
  let last
  while (Date.now() < end) {
    try { const value = await fn(); if (value) return value } catch (e) { last = e }
    await delay(200)
  }
  throw new Error(`Timed out: ${label}${last ? ` (${last.message})` : ''}`)
}
async function step(id, fn) {
  const start = Date.now()
  try { await fn(); results.push({ id, status: 'passed', seconds: (Date.now() - start) / 1000 }); console.log(`PASS ${id}`) }
  catch (e) {
    results.push({ id, status: 'failed', seconds: (Date.now() - start) / 1000, error: e.message })
    await capture('failure').catch(() => {})
    if (page) await page.screenshot({ path: join(out, 'owner-failure.png'), fullPage: true }).catch(() => {})
    throw e
  }
}
async function request(path, options = {}) {
  return fetch(`${control}${path}`, { ...options, signal: AbortSignal.timeout(12000) })
}
async function logs() {
  const response = await request('/api/console')
  assert(response.ok, 'Simulator console unavailable')
  const body = await response.json()
  for (const entry of body.entries) lastId = Math.max(lastId, entry.id)
  return body.entries
}
async function view(screen, predicate = () => true, after = -1) {
  return until(async () => {
    for (const entry of await logs()) {
      if (entry.id <= after || !entry.message.includes('[ledger-glass:view]')) continue
      const start = entry.message.indexOf('{')
      if (start < 0) continue
      let data
      try { data = JSON.parse(entry.message.slice(start)) } catch { continue }
      if (data.screen === screen && predicate(data)) {
        observations.push({ boot, console_id: entry.id, ...data })
        return data
      }
    }
    return false
  }, `rendered ${screen}`)
}
async function input(action, count = 1) {
  for (let i = 0; i < count; i++) {
    const response = await request('/api/input', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ action }) })
    assert(response.ok, `Simulator input ${action}: ${response.status}`)
    await delay(180)
  }
}
async function menu(index) {
  await input('context_menu')
  await delay(400)
  await input('down', index)
  // Item selection starts an OS close animation. App render ACK can arrive
  // before the overlay releases input. Wait for the actual close event.
  await logs()
  const beforeClose = lastId
  await input('click')
  await until(async () => (await logs()).some(entry => {
    if (entry.id <= beforeClose || !entry.message.includes('EvenHub event:')) return false
    try { return JSON.parse(entry.message.slice(entry.message.indexOf('{'))).sysEvent?.eventType === 5 }
    catch { return false }
  }), 'native contextual menu closed', 5000)
}
function alpha(png) {
  const result = Buffer.alloc(png.width * png.height)
  for (let i = 0; i < result.length; i++) result[i] = png.data[i * 4 + 3]
  return result
}
async function capture(name) {
  // Keep the genuine RGBA frame, and wait for native animations to settle.
  let bytes, png, mask, previous, stable = 0
  const deadline = Date.now() + 5000
  do {
    const response = await request('/api/screenshot/glasses')
    assert(response.ok, 'Native framebuffer capture failed')
    bytes = Buffer.from(await response.arrayBuffer())
    png = PNG.sync.read(bytes)
    assert.equal(png.width, 576)
    assert.equal(png.height, 288)
    mask = alpha(png)
    stable = previous?.equals(mask) ? stable + 1 : 0
    previous = mask
    if (stable >= 3) break
    await delay(120)
  } while (Date.now() < deadline)
  const lit = mask.filter(v => v > 0).length
  const filename = `${String(shots.length + 1).padStart(2, '0')}-${name}.png`
  await writeFile(join(out, filename), bytes)
  const preview = new PNG({ width: png.width, height: png.height })
  for (let i = 0; i < mask.length; i++) {
    for (let c = 0; c < 3; c++) preview.data[4*i+c] = Math.round(png.data[4*i+c] * mask[i] / 255)
    preview.data[4*i+3] = 255
  }
  await writeFile(join(out, `preview-${filename}`), PNG.sync.write(preview))
  shots.push({ file: filename, preview: `preview-${filename}`, lit_pixels: lit, source: 'official simulator LVGL RGBA framebuffer' })
  assert(stable >= 3, 'Native framebuffer did not settle; final frame saved')
  assert(lit > 100 && lit < mask.length * 0.95, `Blank/solid native framebuffer: ${lit} lit pixels`)
  return mask
}
function changed(a, b) {
  assert.equal(a.length, b.length)
  let count = 0
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) count++
  assert(count > 20, `Framebuffer did not change (${count} pixels)`)
}
async function startSim() {
  boot++; lastId = 0
  simOutput = createWriteStream(join(runtime, `simulator-${boot}.log`))
  sim = spawn(resolve(root, 'evenhub/node_modules/.bin/evenhub-simulator'), [target, '--automation-port', '9898', '--no-glow'], {
    cwd: runtime, detached: true,
    env: { ...process.env, XDG_CONFIG_HOME: join(runtime, 'config'), XDG_DATA_HOME: join(runtime, 'data'), XDG_CACHE_HOME: join(runtime, 'cache'), RUST_LOG: 'info' },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  sim.stdout.pipe(simOutput); sim.stderr.pipe(simOutput)
  sim.on('error', e => console.error('Simulator launch:', e.message))
  await until(async () => {
    assert(sim.exitCode === null, `Simulator exited with ${sim.exitCode}`)
    const response = await request('/api/ping')
    return response.ok && (await response.text()).includes('pong')
  }, 'official simulator automation port', 60000)
  await view('ready')
}
async function stopSim() {
  if (!sim) return
  try {
    const entries = await logs()
    await writeFile(join(out, `console-${boot}.json`), JSON.stringify(entries.filter(e => !/access[_-]?token|refresh[_-]?token|authorization|cookie|csrf|setlocalstorage|device_code/i.test(e.message)), null, 2))
    const screenshot = await request('/api/screenshot/webview')
    if (screenshot.ok) await writeFile(join(out, `webview-${boot}.png`), Buffer.from(await screenshot.arrayBuffer()))
  } catch {}
  try { process.kill(-sim.pid, 'SIGTERM') } catch {}
  await until(() => sim.exitCode !== null || sim.signalCode !== null, 'simulator exit', 5000).catch(() => { try { process.kill(-sim.pid, 'SIGKILL') } catch {} })
  simOutput.end(); sim = undefined
}
function sql(query) {
  const output = execFileSync('docker', [...dc, 'exec', '-T', 'postgres', 'psql', '-U', 'ledger', '-d', 'ledger', '-Atc', query], { encoding: 'utf8', timeout: 15000 })
  return output.trim()
}
async function pendingDevice() {
  return until(() => {
    const value = sql("SELECT row_to_json(d) FROM (SELECT user_code, client_id, scope FROM oauth_device WHERE status='pending' AND expires_at>now() ORDER BY created_at DESC LIMIT 1) d")
    return value ? JSON.parse(value) : false
  }, 'real pending OAuth request')
}
async function admin(path, method = 'GET', data) {
  const response = await context.request.fetch(`${server}/admin/api${path}`, {
    method, data, headers: { Origin: server, 'X-CSRF-Token': csrf, Accept: 'application/json' }, maxRedirects: 0,
  })
  assert(response.ok(), `Owner API ${method} ${path}: ${response.status()}`)
  return response.status() === 204 ? undefined : response.json()
}
async function decide(code, action = 'approve') {
  await page.goto(`${server}/admin/connect`)
  await page.getByLabel('Connection code').fill(code)
  await page.getByRole('button', { name: 'Review connection', exact: true }).click()
  await page.getByRole('heading', { name: 'Approve this machine?' }).waitFor()
  assert.equal(await page.getByText('Read project memory', { exact: true }).count(), 1)
  assert.equal(await page.getByText('Add and update project memory', { exact: true }).count(), 0)
  await page.screenshot({ path: join(out, `owner-approval-${boot}-${action}.png`), fullPage: true })
  await page.getByRole('button', { name: action === 'approve' ? 'Approve machine' : 'Deny', exact: true }).click()
  await page.locator('p[role=status]').filter({ hasText: action === 'approve' ? 'Machine approved.' : 'Connection denied.' }).waitFor()
}
async function safeConsole() {
  const entries = await logs()
  const errors = entries.filter(e => /\[uncaught\]|\[unhandledrejection\]|input\/render operation failed/.test(e.message))
  assert.equal(errors.length, 0, `Uncaught simulator WebView errors: ${errors.map(e => e.message).join('; ')}`)
}

try {
  await mkdir(out, { recursive: true })
  await step('security.tls-cors', async () => {
    await until(async () => {
      const res = await fetch(`${server}/admin/api/session`, { signal: AbortSignal.timeout(3000) })
      assert.equal(res.status, 401, `Anonymous session endpoint returned ${res.status}`)
      return true
    }, 'HTTPS production stack (anonymous session must be 401)', 90000)
    const unauth = await fetch(`${server}/mcp`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
    assert.equal(unauth.status, 401)
    for (const path of ['/mcp', '/oauth/device', '/oauth/token', '/oauth/register', '/oauth/revoke']) {
      const res = await fetch(server + path, { method: 'OPTIONS', headers: { Origin: 'https://localhost:9443', 'Access-Control-Request-Method': 'POST', 'Access-Control-Request-Headers': 'authorization,content-type,mcp-protocol-version' } })
      assert.equal(res.status, 204, path)
      assert.equal(res.headers.get('access-control-allow-origin'), '*', path)
      assert.equal(res.headers.get('access-control-allow-credentials'), null, path)
    }
    const privateAPI = await fetch(`${server}/admin/api/session`, { headers: { Origin: 'https://localhost:9443' } })
    assert.equal(privateAPI.status, 401)
    assert.equal(privateAPI.headers.get('access-control-allow-origin'), null)
  })
  await step('owner.login', async () => {
    browser = await chromium.launch()
    context = await browser.newContext()
    page = await context.newPage()
    await page.goto(`${server}/admin/connect`)
    const password = page.locator('input[type=password]')
    await password.fill(process.env.GLASS_OWNER_PASSWORD)
    const responsePromise = page.waitForResponse(r => r.url().endsWith('/admin/api/login') && r.request().method() === 'POST')
    await password.press('Enter')
    const response = await responsePromise
    assert.equal(response.status(), 200)
    csrf = (await response.json()).csrf_token
    assert(csrf)
    const bad = await context.request.post(`${server}/admin/api/oauth/device`, { data: { user_code: 'ABCD2345', action: 'approve' }, headers: { Origin: server } })
    assert.equal(bad.status(), 403, 'CSRF is still required with a real owner session')
  })
  await step('pairing.approve-empty', async () => {
    assert.equal((await admin('/projects')).projects.length, 0)
    await startSim()
    await view('pairing')
    await capture('pairing')
    const device = await pendingDevice()
    assert.equal(device.scope, 'ledger:read')
    await decide(device.user_code)
    await view('now', v => v.empty === true)
    await capture('empty-registry')
  })
  await step('now.real-data', async () => {
    for (const [slug, name, tier, hours] of [['ci-focus', 'Atlas CI', 'focus', 20], ['ci-maintain', 'Bravo CI', 'maintain', 10], ['ci-park', 'Dormant CI', 'park', 168]]) {
      await admin(`/projects/${slug}`, 'PUT', { name, tier, hours_wk: hours, goal: 'Verify the real glasses integration', needs_me: 'Review the fixture result', deadline: '20 Sep 2026' })
      await admin(`/projects/${slug}/entries`, 'POST', { kind: 'status', body: `Real PostgreSQL fixture for ${name}.` })
    }
    const after = lastId
    await menu(2)
    const shown = await view('now', v => v.slug === 'ci-focus', after)
    const real = await admin('/projects/ci-focus')
    assert.equal(String(shown.entries[0]), String(real.entries[0].id))
    await capture('now-real-data')
  })
  await step('navigation.first-and-second', async () => {
    let after = lastId
    await input('click')
    await view('projects', v => v.total === 3, after)
    const first = await capture('projects-first')
    after = lastId
    await input('click')
    await view('project', v => v.slug === 'ci-focus', after)
    await capture('detail-first-index-zero')
    after = lastId
    await input('click')
    await view('projects', () => true, after)
    await input('down')
    changed(first, await capture('projects-second'))
    after = lastId
    await input('click')
    await view('project', v => v.slug === 'ci-maintain', after)
    await capture('detail-second')
  })
  await step('navigation.pagination', async () => {
    for (let i = 0; i < 22; i++) await admin(`/projects/ci-extra-${String(i).padStart(2, '0')}`, 'PUT', { name: `Extra ${String(i).padStart(2, '0')} CI`, tier: 'maintain', hours_wk: 1 })
    let after = lastId
    await menu(1)
    const first = await view('projects', v => v.page === 0 && v.total === 25, after)
    assert(first.slugs.includes('next'))
    await input('down', first.slugs.indexOf('next'))
    after = lastId
    await input('click')
    const second = await view('projects', v => v.page === 1, after)
    assert(second.slugs.includes('previous'))
    assert(second.slugs.includes('ci-park'), 'Last project beyond the native 20-item limit is reachable')
    await capture('projects-page-two')
    await input('down', second.slugs.indexOf('ci-park'))
    after = lastId
    await input('click')
    await view('project', v => v.slug === 'ci-park', after)
    await capture('detail-last-project')
  })
  await step('refresh.real-entry', async () => {
    const old = await capture('detail-before-change')
    const entry = await admin('/projects/ci-park/entries', 'POST', { kind: 'decision', body: 'A different owner client changed this entry. Ședință în România; UTF-8 remains intact.' })
    const after = lastId
    await menu(2)
    const shown = await view('project', v => v.slug === 'ci-park', after)
    assert.equal(String(shown.entries[0]), String(entry.id))
    changed(old, await capture('detail-after-real-write'))
  })
  await step('menu.roundtrip', async () => {
    const before = await capture('before-context-menu')
    await input('context_menu')
    await delay(500)
    changed(before, await capture('context-menu'))
    await input('context_menu')
    await delay(500)
    const restored = await capture('context-menu-dismissed')
    assert(restored.equals(before), 'Closing the OS menu preserves the rendered page')
    await safeConsole()
  })
  await step('session.cold-restart', async () => {
    const count = sql('SELECT count(*) FROM oauth_client')
    await stopSim()
    await startSim()
    await view('now', v => v.slug === 'ci-focus')
    assert.equal(sql('SELECT count(*) FROM oauth_client'), count, 'Persisted grant reused, no silent re-pairing')
    await capture('cold-restart')
  })
  await step('scope.write-denied', async () => {
    const reg = await fetch(`${server}/oauth/register`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ client_name: 'System permission probe', grant_types: ['urn:ietf:params:oauth:grant-type:device_code', 'refresh_token'] }) })
    assert.equal(reg.status, 201)
    const { client_id } = await reg.json()
    const d = await fetch(`${server}/oauth/device`, { method: 'POST', body: new URLSearchParams({ client_id, scope: 'ledger:read', resource: `${server}/mcp` }) })
    assert(d.ok)
    const device = await d.json()
    await admin('/oauth/device', 'POST', { user_code: device.user_code, action: 'approve' })
    await delay(device.interval * 1000 + 100)
    const tokenResponse = await fetch(`${server}/oauth/token`, { method: 'POST', body: new URLSearchParams({ grant_type: 'urn:ietf:params:oauth:grant-type:device_code', client_id, device_code: device.device_code }) })
    assert(tokenResponse.ok)
    const pair = await tokenResponse.json()
    assert.equal(pair.scope, 'ledger:read')
    const client = new Client({ name: 'system-read-only-probe', version: '0.1.0' })
    await client.connect(new StreamableHTTPClientTransport(new URL(`${server}/mcp`), { requestInit: { headers: { Authorization: `Bearer ${pair.access_token}` } } }))
    try {
      const before = (await admin('/projects/ci-focus')).entries.length
      const rejected = await client.callTool({ name: 'append_entry', arguments: { slug: 'ci-focus', kind: 'note', body: 'Must never be written' } })
      assert.equal(rejected.isError, true)
      assert.equal((await admin('/projects/ci-focus')).entries.length, before)
    } finally { await client.close(); await admin('/oauth/revoke', 'POST', { client_id }) }
  })
  await step('network.outage-recovery', async () => {
    execFileSync('docker', [...dc, 'stop', 'ledger-mcp'], { timeout: 30000, stdio: 'pipe' })
    try {
      const after = lastId
      await menu(2)
      await view('error', () => true, after)
      await capture('real-mcp-outage')
    } finally { execFileSync('docker', [...dc, 'start', 'ledger-mcp'], { timeout: 30000, stdio: 'pipe' }) }
    await delay(1200)
    const after = lastId
    await menu(2)
    await view('now', v => v.slug === 'ci-focus', after)
    await capture('outage-recovered')
  })
  await step('session.revocation', async () => {
    const clients = (await admin('/oauth/clients')).clients.filter(c => c.client_name === 'Ledger Glass')
    assert.equal(clients.length, 1)
    await admin('/oauth/revoke', 'POST', { client_id: clients[0].client_id })
    const after = lastId
    await menu(2)
    await view('pairing', () => true, after)
    await capture('revoked-requires-approval')
    await decide((await pendingDevice()).user_code)
    await view('now', v => v.slug === 'ci-focus', after)
  })
  await step('pairing.deny', async () => {
    const after = lastId
    await menu(3)
    await view('pairing', () => true, after)
    await decide((await pendingDevice()).user_code, 'deny')
    await view('error', () => true, after)
    await capture('approval-denied')
    await safeConsole()
  })
  await step('exit.dialog', async () => {
    const before = await capture('before-exit-dialog')
    await input('double_click')
    await delay(500)
    changed(before, await capture('system-exit-dialog'))
    await safeConsole()
  })
} catch (error) {
  failed = true
  console.error(error.stack)
} finally {
  await stopSim()
  await browser?.close()
  const missing = planned.filter(id => !results.some(r => r.id === id))
  for (const id of missing) results.push({ id, status: 'not_run', seconds: 0 })
  if (missing.length) failed = true
  const missingFullApp = fullAppRequired.filter(id => !results.some(r => r.id === id && r.status === 'passed'))
  const report = {
    target, tested_commit: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    tests: results, screenshots: shots, observations,
    full_app_complete: !failed && missingFullApp.length === 0, missing_full_app_journeys: missingFullApp,
    real_components: ['production-built plugin; package generation validated separately', 'official native simulator and SDK', 'HTTPS with trusted CI CA', 'production nginx routes', 'all four Ledger Go services', 'production React owner UI in Chromium', 'PostgreSQL/pgvector with real migrations', 'real OAuth approval/token/revoke', 'MCP over real HTTP'],
    substitutes: ['Vendor simulator replaces physical G2/R1/BLE', 'Virtual silent audio input; no speech-recognition claim'],
    not_covered: ['native EHPK installation/loader (0.9.5 URL probe failed)', 'physical R1 event-source identity', 'BLE timing/battery/optical quality', 'Android host permissions and OS process eviction', 'STT accuracy', 'Capture/Recall/Brief/Calendar UI: not implemented yet', 'connected Nextcloud provider and real embedding/reranking model'],
  }
  await writeFile(join(out, 'screenshots.html'), `<!doctype html><meta charset="utf-8"><title>Ledger Glass — simulator evidence</title><h1>Official simulator captures</h1><p>Black previews composite the untouched RGBA frames; these are not hardware photographs.</p>${shots.map(shot => `<figure><img width="576" height="288" src="${shot.preview}"><figcaption>${shot.file}</figcaption></figure>`).join('')}`)
  await writeFile(join(out, 'report.json'), JSON.stringify(report, null, 2))
  const escape = s => String(s).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('"', '&quot;')
  await writeFile(join(out, 'junit.xml'), `<testsuite name="glass-system" tests="${results.length}" failures="${results.filter(r => r.status === 'failed').length}" skipped="${missing.length}">${results.map(r => `<testcase classname="${process.env.GLASS_TARGET}" name="${r.id}" time="${r.seconds}">${r.status === 'failed' ? `<failure message="${escape(r.error)}"/>` : r.status === 'not_run' ? '<skipped message="Earlier prerequisite failed"/>' : ''}</testcase>`).join('')}</testsuite>`)
  const summary = `# Ledger Glass system tests (${process.env.GLASS_TARGET})\n\n${results.map(r => `- ${r.status.toUpperCase()}: ${r.id}`).join('\n')}\n\n**Full app: ${report.full_app_complete ? 'COMPLETE' : 'NOT COMPLETE'}.** Missing executable journeys: ${missingFullApp.join(', ')}. No mocks stand in for those features.\n\nSee report.json for exact tested commit, real components, substitutions and hardware limits.\n`
  await writeFile(join(out, 'summary.md'), summary)
  if (process.env.GITHUB_STEP_SUMMARY) await writeFile(process.env.GITHUB_STEP_SUMMARY, summary, { flag: 'a' })
  if (process.env.GLASS_REQUIRE_FULL_APP === 'true' && !report.full_app_complete) {
    console.error(`Full-app gate blocked: ${missingFullApp.join(', ')}`)
    failed = true
  }
}
process.exitCode = failed ? 1 : 0
