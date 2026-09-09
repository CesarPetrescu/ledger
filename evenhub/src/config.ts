export function normalizeServer(raw: string): string {
  const value = raw.trim()
  if (!value) throw new Error('Ledger server is not configured')

  let url: URL
  try {
    url = new URL(value)
  } catch {
    throw new Error('Ledger server is not a valid URL')
  }

  if (url.protocol !== 'https:') throw new Error('Ledger server must use HTTPS')
  if (url.username || url.password) throw new Error('Ledger server must not contain credentials')
  if (url.search || url.hash) throw new Error('Ledger server must not contain query parameters or a fragment')
  if (url.pathname !== '/' && url.pathname !== '') throw new Error('Ledger server must be an origin without a path')

  return url.origin
}
