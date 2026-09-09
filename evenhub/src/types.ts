export type ProjectTier = 'focus' | 'maintain' | 'park'

export interface ProjectSummary {
  slug: string
  name: string
  tier: ProjectTier
  hours_wk: number
  goal: string
  deadline: string
  last_entry_at?: string | null
}

export interface ProjectEntry {
  id?: number
  slug?: string
  kind: 'decision' | 'note' | 'todo' | 'status'
  body: string
  created_at?: string
  source?: string
}

export interface ProjectDetail {
  project: ProjectSummary & {
    type?: string
    description?: string
    needs_me?: string
    automate?: string
    stack?: string
  }
  entries: ProjectEntry[]
}

export interface ProjectListResult {
  projects: ProjectSummary[]
}
