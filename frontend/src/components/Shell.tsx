import { useEffect, useState, type ReactNode } from 'react'
import { describeError } from '../api'
import { useAuth } from '../auth'
import { Link, navigate, useLocation } from '../router'
import { OverflowMenu } from './OverflowMenu'
import { useToast } from './Toast'
import { BrandMark, Icon, formatRelative, type IconName } from './ui'
import { useLiveUpdates } from '../live'
import { applyTheme, readTheme, THEME_ORDER, type ThemeChoice } from '../theme'

const THEME_LABEL: Record<ThemeChoice, string> = { system: 'System', light: 'Light', dark: 'Dark' }

const NAV: { to: string; label: string; icon: IconName; match: (path: string) => boolean; mobileHidden?: boolean }[] = [
  { to: '/', label: 'Inbox', icon: 'inbox', match: (path) => path === '/' },
  { to: '/projects', label: 'Projects', icon: 'projects', match: (path) => path.startsWith('/projects') },
  { to: '/table', label: 'Table', icon: 'table', match: (path) => path === '/table' },
  { to: '/calendar', label: 'Calendar', icon: 'calendar', match: (path) => path === '/calendar' },
  { to: '/handoffs', label: 'Handoffs', icon: 'handoffs', match: (path) => path.startsWith('/handoffs') },
  { to: '/search', label: 'Search', icon: 'search', match: (path) => path === '/search', mobileHidden: true },
  { to: '/agents', label: 'Agents', icon: 'agents', match: (path) => path === '/agents' },
  // Phones reach Access from the account menu in the top bar; the bottom bar has no room for it.
  { to: '/access', label: 'Access', icon: 'clients', match: (path) => path === '/access' || path === '/clients' || path === '/connect', mobileHidden: true },
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
  const [theme, setTheme] = useState<ThemeChoice>(readTheme)
  const nextTheme = THEME_ORDER[(THEME_ORDER.indexOf(theme) + 1) % THEME_ORDER.length]!
  const cycleTheme = () => {
    applyTheme(nextTheme)
    setTheme(nextTheme)
  }

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
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <p className="topbar-title">{title}</p>
        <p className="live-status" data-status={live} aria-live="polite">
          <Icon name={live === 'live' ? 'live' : 'offline'} />
          <span>{live === 'live' ? 'Live' : live === 'connecting' ? 'Connecting' : 'Offline'}</span>
        </p>
        <Link to="/help" className="icon-button help-link" aria-label="Help: how Ledger works" title="Help" aria-current={path === '/help' ? 'page' : undefined}>
          <Icon name="help" />
        </Link>
        <button type="button" className="icon-button theme-toggle" onClick={cycleTheme}
          aria-label={`Theme: ${THEME_LABEL[theme]}. Switch to ${THEME_LABEL[nextTheme]}`} title={`Theme: ${THEME_LABEL[theme]}`}>
          <Icon name={theme} />
        </button>
        <button type="button" className="search-trigger" onClick={() => navigate('/search')}>
          <Icon name="search" />
          <span>Search</span>
          <kbd>{isMac ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
        {/* Phones only: the top bar keeps one menu instead of separate Help, theme, and sign-out buttons. */}
        <div className="account-menu">
          <OverflowMenu label="Account menu" items={[
            { label: 'Help', onSelect: () => navigate('/help') },
            { label: `Theme: ${THEME_LABEL[theme]} (switch to ${THEME_LABEL[nextTheme]})`, onSelect: cycleTheme },
            { label: 'Access', onSelect: () => navigate('/access') },
            { label: signingOut ? 'Signing out…' : 'Sign out', onSelect: () => void handleSignOut(), disabled: signingOut },
          ]} />
        </div>
      </header>
      <aside id="sidebar" className="sidebar">
        <div className="brand">
          <BrandMark />
          <span className="wordmark nav-label">Ledger</span>
        </div>
        <nav aria-label="Primary" className="primary-nav">
          {NAV.map((item) => (
            <Link key={item.to} to={item.to} className={item.mobileHidden ? 'mobile-hidden' : undefined} aria-current={item.match(path) ? 'page' : undefined} title={item.label}>
              <Icon name={item.icon} />
              <span className="nav-label">{item.label}</span>
            </Link>
          ))}
        </nav>
        <div className="sidebar-foot">
          {expires && (
            <p className="muted small">
              Session ends <time dateTime={expires}>{formatRelative(expires)}</time>
            </p>
          )}
          <button type="button" className="btn btn-quiet" aria-label={signingOut ? 'Signing out' : 'Sign out'} title="Sign out" disabled={signingOut} onClick={() => void handleSignOut()}>
            <Icon name="logout" /> <span className="nav-label">{signingOut ? 'Signing out…' : 'Sign out'}</span>
          </button>
        </div>
      </aside>
      <main id="main" className="content" tabIndex={-1}>
        {children}
      </main>
    </div>
  )
}
