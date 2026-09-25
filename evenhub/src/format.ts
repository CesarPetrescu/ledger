import type { ProjectDetail, ProjectEntry, ProjectSummary } from './types'

const TIER_ORDER: Record<ProjectSummary['tier'], number> = { focus: 0, maintain: 1, park: 2 }

export function sortProjects(projects: ProjectSummary[]): ProjectSummary[] {
  return [...projects].sort((a, b) => {
    const tier = TIER_ORDER[a.tier] - TIER_ORDER[b.tier]
    if (tier !== 0) return tier
    const hours = b.hours_wk - a.hours_wk
    if (hours !== 0) return hours
    return a.name.localeCompare(b.name)
  })
}

export function chooseNowProject(projects: ProjectSummary[]): ProjectSummary | null {
  return sortProjects(projects)[0] ?? null
}

export function projectListLabel(project: ProjectSummary): string {
  const prefix = project.tier === 'focus' ? 'FOCUS · ' : project.tier === 'maintain' ? '' : 'PARK · '
  return truncate(`${prefix}${project.name}`, 62)
}

export function formatNow(detail: ProjectDetail): string {
  const project = detail.project
  const header = ['LEDGER · NOW', '', project.name.toUpperCase(), project.tier.toUpperCase()]
  if (project.hours_wk > 0) header[3] += ` · ${project.hours_wk}h/wk`
  if (project.deadline) header.push(`Due: ${project.deadline}`)

  const lines = [...header]
  if (project.goal) lines.push('', `Goal: ${clean(project.goal)}`)

  const important = detail.entries.find(entry => entry.kind === 'todo' || entry.kind === 'status' || entry.kind === 'decision')
  if (important) lines.push('', `${important.kind.toUpperCase()}: ${clean(important.body)}`)

  return clamp(lines.join('\n'), 460)
}

export function formatProject(detail: ProjectDetail): string {
  const project = detail.project
  const lines = [project.name.toUpperCase(), `${project.tier.toUpperCase()}${project.hours_wk ? ` · ${project.hours_wk}h/wk` : ''}`]
  if (project.deadline) lines.push(`Due: ${project.deadline}`)
  if (project.goal) lines.push('', `Goal: ${clean(project.goal)}`)
  if (project.needs_me) lines.push(`Needs me: ${clean(project.needs_me)}`)

  for (const entry of detail.entries.slice(0, 2)) {
    lines.push('', `${entry.kind.toUpperCase()}: ${clean(entry.body)}`)
  }
  return clamp(lines.join('\n'), 480)
}

export function formatError(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error)
  return clamp(`LEDGER GLASS\n\nConnection failed\n\n${clean(message)}\n\nUse the menu to retry or reconnect.`, 460)
}

export function summarizeEntry(entry: ProjectEntry): string {
  return `${entry.kind.toUpperCase()}: ${clean(entry.body)}`
}

function clean(value: string): string {
  return value.replace(/\s+/g, ' ').trim()
}

function truncate(value: string, max: number): string {
  if (value.length <= max) return value
  return `${value.slice(0, Math.max(0, max - 1)).trimEnd()}…`
}

function clamp(value: string, max: number): string {
  return truncate(value, max)
}
