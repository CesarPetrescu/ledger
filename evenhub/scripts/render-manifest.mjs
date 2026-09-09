import { writeFileSync } from 'node:fs'

function ledgerOrigin(raw) {
  if (!raw) throw new Error('LEDGER_SERVER is required')
  const url = new URL(raw)
  if (url.protocol !== 'https:') throw new Error('LEDGER_SERVER must use HTTPS')
  if (url.username || url.password || url.search || url.hash) throw new Error('LEDGER_SERVER must be a plain HTTPS origin')
  if (url.pathname !== '/' && url.pathname !== '') throw new Error('LEDGER_SERVER must not contain a path')
  return url.origin
}

const origin = ledgerOrigin(process.env.LEDGER_SERVER)
const manifest = {
  package_id: 'com.cesarpetrescu.ledgerglass',
  edition: '202601',
  name: 'Ledger Glass',
  version: '0.1.0',
  min_app_version: '2.2.10',
  min_sdk_version: '0.0.15',
  entrypoint: 'index.html',
  permissions: [
    {
      name: 'network',
      desc: 'Connects to the configured Ledger server for read-only project memory.',
      whitelist: [origin],
    },
  ],
  supported_languages: ['en'],
}

writeFileSync(new URL('../app.json', import.meta.url), `${JSON.stringify(manifest, null, 2)}\n`)
console.log(`Rendered app.json for ${origin}`)
