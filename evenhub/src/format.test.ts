import { describe, expect, it } from 'vitest'
import { chooseNowProject, formatNow, projectListLabel, sortProjects } from './format'
import type { ProjectDetail, ProjectSummary } from './types'

const projects: ProjectSummary[] = [
  { slug: 'archive', name: 'Archive', tier: 'park', hours_wk: 0, goal: '', deadline: '' },
  { slug: 'ops', name: 'Operations', tier: 'maintain', hours_wk: 8, goal: 'Keep it healthy', deadline: '' },
  { slug: 'atlas', name: 'Atlas', tier: 'focus', hours_wk: 12, goal: 'Ship', deadline: '20 Sep 2026' },
]

describe('project ordering', () => {
  it('puts focus first, then maintain, then park', () => {
    expect(sortProjects(projects).map(project => project.slug)).toEqual(['atlas', 'ops', 'archive'])
    expect(chooseNowProject(projects)?.slug).toBe('atlas')
  })

  it('keeps list labels inside the G2 limit', () => {
    const label = projectListLabel({ ...projects[2], name: 'A'.repeat(200) })
    expect(label.length).toBeLessThanOrEqual(62)
  })
})

describe('formatNow', () => {
  it('renders project state and a recent meaningful entry compactly', () => {
    const detail: ProjectDetail = {
      project: projects[2],
      entries: [{ kind: 'status', body: 'Integration is ready for final verification.' }],
    }
    const rendered = formatNow(detail)
    expect(rendered).toContain('ATLAS')
    expect(rendered).toContain('20 Sep 2026')
    expect(rendered).toContain('STATUS:')
    expect(rendered.length).toBeLessThanOrEqual(460)
  })
})
