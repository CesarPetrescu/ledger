import { useEffect, useState, type ReactNode } from 'react'
import { useAuth } from '../auth'
import { Link, navigate, useLocation } from '../router'
import { Icon, formatRelative } from './ui'

const NAV = [
  { to: '/', label: 'Overview', match: (path: string) => path === '/' },
  { to: '/projects', label: 'Projects', match: (path: string) => path.startsWith('/projects') },
  { to: '/search', label: 'Search', match: (path: string) => path === '/search' },
  { to: '/clients', label: 'Clients', match: (path: string) => path === '/clients' },
]

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

export function Shell({ title, children }: { title: string; children: ReactNode }) {
  const { state, signOut } = useAuth()
  const { path } = useLocation()
  const [navOpen, setNavOpen] = useState(false)

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const palette = (event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === 'k'
      const slash = event.key === '/' && !event.ctrlKey && !event.metaKey && !event.altKey && !isTypingTarget(event.target)
      if (palette || slash) {
        event.preventDefault()
        navigate('/search')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const expires = state.status === 'authenticated' ? state.expiresAt : ''
  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

  return (
    <div className="shell" data-nav-open={navOpen || undefined}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <button type="button" className="icon-button nav-toggle" aria-label={navOpen ? 'Close navigation' : 'Open navigation'} aria-expanded={navOpen} aria-controls="sidebar" onClick={() => setNavOpen((open) => !open)}>
          <Icon name={navOpen ? 'close' : 'menu'} />
        </button>
        <p className="topbar-title">{title}</p>
        <button type="button" className="search-trigger" onClick={() => navigate('/search')}>
          <Icon name="search" />
          <span>Search</span>
          <kbd>{isMac ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
      </header>
      <aside id="sidebar" className="sidebar">
        <div className="brand">
          <span className="wordmark">Ledger</span>
          <span className="brand-sub">admin</span>
        </div>
        <nav aria-label="Primary" className="primary-nav">
          {NAV.map((item) => (
            <Link key={item.to} to={item.to} aria-current={item.match(path) ? 'page' : undefined} onClick={() => setNavOpen(false)}>
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="sidebar-foot">
          {expires && (
            <p className="muted small">
              Session ends <time dateTime={expires}>{formatRelative(expires)}</time>
            </p>
          )}
          <button type="button" className="btn btn-quiet" onClick={() => void signOut()}>
            <Icon name="logout" /> Sign out
          </button>
        </div>
      </aside>
      {navOpen && <button type="button" className="backdrop" aria-label="Close navigation" onClick={() => setNavOpen(false)} />}
      <main id="main" className="content" tabIndex={-1}>
        {children}
      </main>
    </div>
  )
}
