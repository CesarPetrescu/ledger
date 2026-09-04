import { useEffect, useState, type ReactNode } from 'react'
import { describeError } from '../api'
import { useAuth } from '../auth'
import { Link, navigate, useLocation } from '../router'
import { useToast } from './Toast'
import { Icon, formatRelative, type IconName } from './ui'
import { useLiveUpdates } from '../live'

const NAV: { to: string; label: string; icon: IconName; match: (path: string) => boolean }[] = [
  { to: '/', label: 'Overview', icon: 'home', match: (path) => path === '/' },
  { to: '/projects', label: 'Projects', icon: 'projects', match: (path) => path.startsWith('/projects') },
  { to: '/calendar', label: 'Calendar', icon: 'calendar', match: (path) => path === '/calendar' },
  { to: '/search', label: 'Search', icon: 'search', match: (path) => path === '/search' },
  { to: '/clients', label: 'Agents', icon: 'clients', match: (path) => path === '/clients' },
]

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

export function Shell({ title, children }: { title: string; children: ReactNode }) {
  const { state, signOut } = useAuth()
  const { path } = useLocation()
  const [signingOut, setSigningOut] = useState(false)
  const toast = useToast()
  const live = useLiveUpdates()

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
  const handleSignOut = async () => {
    setSigningOut(true)
    try {
      await signOut()
    } catch (failure) {
      toast(describeError(failure), 'error')
    } finally {
      setSigningOut(false)
    }
  }

  return (
    <div className={path === '/search' ? 'shell shell-search' : 'shell'}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <p className="topbar-title">{title}</p>
        <p className="live-status" data-status={live} aria-live="polite">
          <Icon name={live === 'live' ? 'live' : 'offline'} />
          <span>{live === 'live' ? 'Live' : live === 'connecting' ? 'Connecting' : 'Offline'}</span>
        </p>
        <button type="button" className="search-trigger" onClick={() => navigate('/search')}>
          <Icon name="search" />
          <span>Search</span>
          <kbd>{isMac ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
        <button type="button" className="icon-button mobile-signout" aria-label="End session" title="Sign out" disabled={signingOut} onClick={() => void handleSignOut()}>
          <Icon name="logout" />
        </button>
      </header>
      <aside id="sidebar" className="sidebar">
        <div className="brand">
          <span className="brand-mark"><Icon name="book" /></span>
          <span className="wordmark">Ledger</span>
        </div>
        <nav aria-label="Primary" className="primary-nav">
          {NAV.map((item) => (
            <Link key={item.to} to={item.to} aria-current={item.match(path) ? 'page' : undefined}>
              <Icon name={item.icon} />
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="sidebar-foot">
          <p className="live-status sidebar-live" data-status={live}>
            <Icon name={live === 'live' ? 'live' : 'offline'} />
            <span>{live === 'live' ? 'Live updates on' : live === 'connecting' ? 'Connecting…' : 'Updates offline'}</span>
          </p>
          {expires && (
            <p className="muted small">
              Session ends <time dateTime={expires}>{formatRelative(expires)}</time>
            </p>
          )}
          <button type="button" className="btn btn-quiet" disabled={signingOut} onClick={() => void handleSignOut()}>
            <Icon name="logout" /> {signingOut ? 'Signing out…' : 'Sign out'}
          </button>
        </div>
      </aside>
      <main id="main" className="content" tabIndex={-1}>
        {children}
      </main>
    </div>
  )
}
