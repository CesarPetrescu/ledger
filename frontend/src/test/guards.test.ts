import { describe, expect, it } from 'vitest'

// Source files are imported as raw text through Vite so the guards need no Node APIs.
const sources = import.meta.glob<string>(['../**/*.{ts,tsx,css}', '../../index.html', '!../test/**'], { query: '?raw', import: 'default', eager: true })
const fonts = import.meta.glob<string>('../assets/fonts/*', { query: '?url', import: 'default', eager: true })
const license = import.meta.glob<string>('../assets/fonts/OFL.txt', { query: '?raw', import: 'default', eager: true })

describe('untrusted-content and asset guards', () => {
  const files = Object.entries(sources)

  it('covers the application sources', () => {
    expect(files.length).toBeGreaterThan(10)
    expect(files.some(([name]) => name.endsWith('index.html'))).toBe(true)
  })

  it('never renders memory content as HTML or uses inline styles', () => {
    for (const [file, body] of files) {
      expect(body, file).not.toMatch(/dangerouslySetInnerHTML/)
      expect(body, file).not.toMatch(/\.innerHTML\s*=/)
      expect(body, file).not.toMatch(/style=\{/)
      expect(body, file).not.toMatch(/<style/)
      expect(body, file).not.toMatch(/\son\w+="/)
    }
  })

  it('loads no runtime assets from third-party origins', () => {
    for (const [file, body] of files) {
      expect(body, file).not.toMatch(/https?:\/\/(fonts\.|cdn\.|unpkg|jsdelivr|googleapis|gstatic)/)
      expect(body, file).not.toMatch(/@import\s+url\(\s*['"]?https?:/)
    }
    const html = files.find(([name]) => name.endsWith('index.html'))?.[1] ?? ''
    expect(html).not.toMatch(/<script(?![^>]*src=)[^>]*>[^<]/)
    expect(html).not.toMatch(/<link[^>]+href="https?:/)
  })

  it('bundles the Sora font with its license', () => {
    const names = Object.keys(fonts).map((path) => path.split('/').pop())
    expect(names).toEqual(expect.arrayContaining(['Sora-latin.woff2', 'Sora-latin-ext.woff2', 'OFL.txt']))
    expect(Object.values(license)[0]).toMatch(/SIL Open Font License/)
  })
})
