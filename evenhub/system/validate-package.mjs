import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'

const root = process.cwd()
const out = process.env.GLASS_ARTIFACT_DIR
const runtime = process.env.GLASS_RUNTIME_DIR
assert(out && runtime && process.env.LEDGER_SERVER === 'https://localhost:8443', 'Disposable CI deployment required')
const app = JSON.parse(await readFile('evenhub/app.json', 'utf8'))
assert.equal(app.entrypoint, 'index.html')
assert.equal(app.min_sdk_version, '0.0.15')
assert.deepEqual(app.permissions.find(p => p.name === 'network')?.whitelist, [process.env.LEDGER_SERVER])
const files = []
async function walk(dir, prefix = '') {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    assert(!entry.isSymbolicLink(), 'Build contains a symlink')
    const rel = prefix + entry.name
    if (entry.isDirectory()) await walk(join(dir, entry.name), rel + '/')
    else {
      assert(!/(^|\/)(\.env|credentials|secrets)/i.test(rel), 'Private build input')
      const bytes = await readFile(join(dir, entry.name))
      files.push({ path: rel, bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') })
    }
  }
}
await walk('evenhub/dist')
assert(files.some(f => f.path === app.entrypoint && f.bytes > 0))
assert(files.some(f => f.path.endsWith('.js') && f.bytes > 0))
const bytes = await readFile('evenhub/ledger-glass.ehpk')
assert.equal(bytes.subarray(0, 4).toString('ascii'), 'EHPK', 'Official packer did not produce an EHPK file')
assert(bytes.length > 1024, 'Suspiciously empty EHPK package')
const negative = []
for (const [name, invalid] of [
  ['missing-entrypoint', { ...app, entrypoint: 'does-not-exist.html' }],
  ['invalid-package-id', { ...app, package_id: 'INVALID PACKAGE ID' }],
]) {
  const manifest = join(runtime, `${name}.json`)
  const destination = join(runtime, `${name}.ehpk`)
  await writeFile(manifest, JSON.stringify(invalid))
  const result = spawnSync(resolve(root, 'evenhub/node_modules/.bin/evenhub'), ['pack', manifest, resolve(root, 'evenhub/dist'), '-o', destination, '--sdk-ver', '0.0.15'], { encoding: 'utf8', timeout: 30000 })
  assert(!result.error, `Packer negative case failed to execute: ${result.error?.message}`)
  assert(result.status !== null && result.status !== 0, `Packer accepted ${name}`)
  let exists = false
  try { await readFile(destination); exists = true } catch (e) { if (e.code !== 'ENOENT') throw e }
  assert(!exists, `Rejected ${name} still wrote a package`)
  negative.push({ name, rejected: true, exit_code: result.status })
}
await mkdir(out, { recursive: true })
await writeFile(join(out, 'package-verification.json'), JSON.stringify({
  producer: '@evenrealities/evenhub-cli@0.1.14',
  manifest: app,
  package: { bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex'), header: 'EHPK' },
  packer_inputs: files.sort((a, b) => a.path.localeCompare(b.path)),
  negative_cases: negative,
  installation_tested: false,
  limitation: 'Simulator 0.9.5 treated the EHPK URL as a blank WebView. No official unpack API is exported. Native journeys use the exact production build supplied to the official packer; actual Even App package installation requires hardware acceptance.',
}, null, 2))
console.log('PASS official EHPK production and invalid-input rejection; native installation NOT claimed')
