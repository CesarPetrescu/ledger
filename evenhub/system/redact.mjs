import { readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
const [source, destination] = process.argv.slice(2)
const sensitive = Object.entries(process.env).filter(([k]) => /PASSWORD|TOKEN|ENCRYPTION_KEY/.test(k)).map(([,v]) => v).filter(v => v && v.length > 5)
for (const file of await readdir(source)) {
  if (!file.endsWith('.log')) continue
  const raw = await readFile(join(source, file), 'utf8')
  // Drop entire credential/bridge-storage lines, including escaped JSON payloads.
  const clean = raw.split('\n').filter(line => !/access[_-]?token|refresh[_-]?token|authorization:|setlocalstorage|set[_-]local[_-]storage|cookie|csrf[_-]token|device_code|BEGIN .*PRIVATE KEY/i.test(line)).map(line => sensitive.reduce((s, secret) => s.replaceAll(secret, '[REDACTED]'), line)).join('\n')
  await writeFile(join(destination, file), clean)
}
