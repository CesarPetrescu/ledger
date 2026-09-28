import { useState } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

// Handoff text is written by agents: Markdown is rendered without any raw
// HTML (skipHtml), links open in a new tab without referrer, unsafe URL
// schemes are dropped by react-markdown, and remote images are shown as links
// so opening a message never loads a tracker.
export function MarkdownText({ text }: { text: string }) {
  return (
    <div className="markdown">
      <Markdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          a: ({ href, children }) => <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>,
          img: ({ src, alt }) => (typeof src === 'string' && src ? <a href={src} target="_blank" rel="noopener noreferrer">{alt || 'Image'} (image link)</a> : <span>{alt}</span>),
        }}
      >
        {text}
      </Markdown>
    </div>
  )
}

const KEY = 'ledger.markdown-preview'

/** Whether handoffs show formatted Markdown; remembered on this browser only. */
export function useMarkdownPreview(): [boolean, (on: boolean) => void] {
  const [on, setOn] = useState(() => {
    try { return localStorage.getItem(KEY) !== 'off' } catch { return true }
  })
  const set = (value: boolean) => {
    setOn(value)
    try { localStorage.setItem(KEY, value ? 'on' : 'off') } catch { /* private mode: the choice lasts this page */ }
  }
  return [on, set]
}

export function MarkdownToggle({ on, onChange }: { on: boolean; onChange: (on: boolean) => void }) {
  return (
    <label className="markdown-toggle">
      <input type="checkbox" role="switch" checked={on} onChange={(event) => onChange(event.target.checked)} />
      <span>Markdown preview</span>
    </label>
  )
}
