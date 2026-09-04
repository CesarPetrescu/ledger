import type { ComponentType, ReactNode } from 'react'
import {
  IconAlertTriangle,
  IconArrowLeft,
  IconBook2,
  IconEye,
  IconEyeOff,
  IconFolder,
  IconHome,
  IconKey,
  IconLogout,
  IconMenu2,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconWifi,
  IconWifiOff,
  IconX,
  type IconProps,
} from '@tabler/icons-react'

export type IconName = 'eye' | 'eye-off' | 'menu' | 'close' | 'search' | 'logout' | 'plus' | 'alert' | 'back' | 'refresh' | 'book' | 'home' | 'projects' | 'clients' | 'live' | 'offline'

const icons: Record<IconName, ComponentType<IconProps>> = {
  eye: IconEye,
  'eye-off': IconEyeOff,
  menu: IconMenu2,
  close: IconX,
  search: IconSearch,
  logout: IconLogout,
  plus: IconPlus,
  alert: IconAlertTriangle,
  back: IconArrowLeft,
  refresh: IconRefresh,
  book: IconBook2,
  home: IconHome,
  projects: IconFolder,
  clients: IconKey,
  live: IconWifi,
  offline: IconWifiOff,
}

export function Icon({ name, ...props }: { name: IconName } & IconProps) {
  const Component = icons[name]
  return <Component aria-hidden="true" focusable="false" size={18} stroke={1.8} {...props} />
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
