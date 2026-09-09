import { describe, expect, it } from 'vitest'
import { normalizeServer } from './config'

describe('normalizeServer', () => {
  it('normalizes an HTTPS origin', () => {
    expect(normalizeServer('https://ledger.example.com/')).toBe('https://ledger.example.com')
  })

  it.each([
    'http://ledger.example.com',
    'https://user:pass@ledger.example.com',
    'https://ledger.example.com/path',
    'https://ledger.example.com/?x=1',
  ])('rejects unsafe server value %s', (value: string) => {
    expect(() => normalizeServer(value)).toThrow()
  })
})
