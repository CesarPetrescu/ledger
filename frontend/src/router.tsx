import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from 'react'

// Minimal history router. The app is mounted under /admin/ on the same origin as the API.
const BASE = '/admin'
const listeners = new Set<() => void>()

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  window.addEventListener('popstate', listener)
  return () => {
    listeners.delete(listener)
    window.removeEventListener('popstate', listener)
  }
}

function snapshot(): string {
  return window.location.pathname + window.location.search
}

export function href(to: string): string {
  return BASE + (to.startsWith('/') ? to : `/${to}`)
}

export function navigate(to: string, options: { replace?: boolean } = {}): void {
  const url = href(to)
  if (options.replace) {
    window.history.replaceState(null, '', url)
  } else {
    window.history.pushState(null, '', url)
  }
  for (const listener of listeners) listener()
}

export interface Location {
  path: string
  query: URLSearchParams
}

export function useLocation(): Location {
  const current = useSyncExternalStore(subscribe, snapshot, snapshot)
  const url = new URL(current, 'http://localhost')
  let path = url.pathname.startsWith(BASE) ? url.pathname.slice(BASE.length) : url.pathname
  if (path === '' || path === '/') path = '/'
  if (path.length > 1 && path.endsWith('/')) path = path.slice(0, -1)
  return { path, query: url.searchParams }
}

type LinkProps = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'href'> & { to: string }

export function Link({ to, onClick, ...props }: LinkProps) {
  const handleClick = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event)
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    navigate(to)
  }
  return <a href={href(to)} onClick={handleClick} {...props} />
}
