import type { ReactNode, SVGProps } from 'react'

type IconName = 'eye' | 'eye-off' | 'menu' | 'close' | 'search' | 'logout' | 'plus' | 'alert' | 'back' | 'refresh'

const paths: Record<IconName, ReactNode> = {
  eye: (
    <>
      <path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12Z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  'eye-off': (
    <>
      <path d="M3 3l18 18" />
      <path d="M10.6 5.2A10.6 10.6 0 0 1 12 5c6.5 0 10 7 10 7a17.6 17.6 0 0 1-3.2 4.1M6.6 6.6C3.6 8.6 2 12 2 12s3.5 7 10 7a10 10 0 0 0 4.4-1" />
      <path d="M9.9 9.9a3 3 0 0 0 4.2 4.2" />
    </>
  ),
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  close: <path d="M6 6l12 12M18 6L6 18" />,
  search: (
    <>
      <circle cx="11" cy="11" r="7" />
      <path d="m20 20-3.5-3.5" />
    </>
  ),
  logout: (
    <>
      <path d="M10 4H5v16h5" />
      <path d="M14 8l4 4-4 4M18 12H9" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  alert: (
    <>
      <path d="M12 3 2 20h20L12 3Z" />
      <path d="M12 10v4M12 17h.01" />
    </>
  ),
  back: <path d="M15 5l-7 7 7 7" />,
  refresh: (
    <>
      <path d="M20 12a8 8 0 1 1-2.3-5.7" />
      <path d="M20 4v5h-5" />
    </>
  ),
}

export function Icon({ name, ...props }: { name: IconName } & SVGProps<SVGSVGElement>) {
  return (
    <svg aria-hidden="true" focusable="false" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" {...props}>
      {paths[name]}
    </svg>
  )
}

export function TierBadge({ tier }: { tier: string }) {
  return (
    <span className="badge" data-tier={tier}>
      {tier}
    </span>
  )
}

export function KindBadge({ kind }: { kind: string }) {
  return (
    <span className="badge" data-kind={kind}>
      {kind}
    </span>
  )
}

const absolute = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const relative = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

export function formatRelative(iso: string, now = Date.now()): string {
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return iso
  const seconds = Math.round((then - now) / 1000)
  const abs = Math.abs(seconds)
  if (abs < 60) return relative.format(seconds, 'second')
  if (abs < 3600) return relative.format(Math.round(seconds / 60), 'minute')
  if (abs < 86400) return relative.format(Math.round(seconds / 3600), 'hour')
  if (abs < 86400 * 30) return relative.format(Math.round(seconds / 86400), 'day')
  return absolute.format(new Date(then))
}

export function Timestamp({ iso }: { iso: string }) {
  const parsed = Date.parse(iso)
  return (
    <time dateTime={iso} title={Number.isNaN(parsed) ? iso : absolute.format(new Date(parsed))}>
      {formatRelative(iso)}
    </time>
  )
}

export function Loading({ label }: { label: string }) {
  return (
    <p className="muted loading" aria-busy="true">
      {label}
    </p>
  )
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: (() => void) | undefined }) {
  return (
    <div className="state state-error" role="alert">
      <Icon name="alert" />
      <p>{message}</p>
      {onRetry && (
        <button type="button" className="btn" onClick={onRetry}>
          <Icon name="refresh" /> Retry
        </button>
      )}
    </div>
  )
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="state state-empty">{children}</div>
}

export function StaleNotice({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <p className="notice notice-stale" role="alert">
      {message}{' '}
      <button type="button" className="link-button" onClick={onRetry}>
        Retry
      </button>
    </p>
  )
}
