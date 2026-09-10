// Documentation capture against the disposable production stack, after the
// native tests have passed. No DOM rewriting or intercepted backend responses.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { chromium } from 'playwright'

const server = process.env.LEDGER_PUBLIC_URL
const out = process.env.GLASS_ARTIFACT_DIR
assert(server === 'https://localhost:8443' && out && process.env.GLASS_OWNER_PASSWORD,
  'README capture must only run against the disposable system-test deployment')
const browser = await chromium.launch()
try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 1 })
  const page = await context.newPage()
  await page.goto(`${server}/admin/`)
  const password = page.locator('input[type=password]')
  await password.fill(process.env.GLASS_OWNER_PASSWORD)
  const loggedIn = page.waitForResponse(r => r.url().endsWith('/admin/api/login') && r.request().method() === 'POST')
  await password.press('Enter')
  const login = await loggedIn
  assert.equal(login.status(), 200)
  const csrf = (await login.json()).csrf_token
  assert(csrf, 'Real owner session did not issue a CSRF token')
  async function owner(path, method, data) {
    const response = await context.request.fetch(`${server}/admin/api${path}`, {
      method, data, maxRedirects: 0,
      headers: { Origin: server, 'X-CSRF-Token': csrf, Accept: 'application/json' },
    })
    assert(response.ok(), `README fixture ${method} ${path}: ${response.status()}`)
  }
  const projects = [
    ['readme-atlas', { name: 'Atlas', tier: 'focus', hours_wk: 12, type: 'Product',
      goal: 'Keep project context in sync across people, devices and agents.',
      description: 'A shared memory for decisions, next actions and progress.',
      needs_me: 'Review the release checklist', automate: 'Build verification and daily brief',
      stack: 'Go · PostgreSQL · React · Kotlin', deadline: 'Next release' }],
    ['readme-home-lab', { name: 'Home lab', tier: 'maintain', hours_wk: 3,
      goal: 'Keep backups verified and services observable.' }],
    ['readme-learning', { name: 'Learning notes', tier: 'park', hours_wk: 1,
      goal: 'Save useful references and experiments for later.' }],
  ]
  for (const [slug, project] of projects) await owner(`/projects/${slug}`, 'PUT', project)
  for (const [kind, body] of [
    ['decision', 'Keep one source of truth. Every client reads and contributes to the same project history.'],
    ['todo', 'Review the latest changes, then pick the next useful action.'],
    ['status', 'Web, Android and glasses clients are connected to the same Ledger.'],
  ]) await owner('/projects/readme-atlas/entries', 'POST', { kind, body })
  await page.goto(`${server}/admin/projects/readme-atlas`)
  await page.getByLabel('Filter projects', { exact: true }).fill('readme-')
  await page.getByRole('heading', { name: 'Atlas', exact: true }).waitFor()
  await page.getByText('Keep project context in sync across people, devices and agents.', { exact: true }).waitFor()
  await page.evaluate(() => document.fonts.ready)
  await page.screenshot({ path: join(out, 'readme-website.png'), animations: 'disabled' })
  const { readFile } = await import('node:fs/promises')
  const bytes = await readFile(join(out, 'readme-website.png'))
  await writeFile(join(out, 'readme-website.json'), JSON.stringify({
    source_commit: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(),
    surface: 'Production React web client in Chromium; real HTTPS Ledger and PostgreSQL',
    screen: 'Project browser and Atlas project overview', fixture_data: true,
    viewport: { width: 1440, height: 1000 },
    sha256: createHash('sha256').update(bytes).digest('hex'),
    image_modified: false,
  }, null, 2))
  console.log('PASS README website screenshot from real owner UI and disposable project data')
} finally {
  await browser.close()
}
