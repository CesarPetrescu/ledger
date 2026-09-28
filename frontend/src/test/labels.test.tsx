import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { TableEntry } from '../api'
import { atlas, authenticatedSession, beacon, mockApi, noteEntry, renderApp } from './helpers'

const owner = { read: false, starred: false, handled: false }
const now = new Date().toISOString()
const decision: TableEntry = { ...noteEntry, owner, id: '60', kind: 'decision', body: 'Chose Stripe over Paddle for $49/mo plans. Docs: https://stripe.com/docs', created_at: now, project_name: 'Atlas',
  meta: { title: 'Use Stripe', tags: ['billing'], refs: [], origin: 'model', importance: 'useful', category: 'payments', unsure: ['importance'],
    details: { chosen: 'Stripe', rejected: 'Paddle', numbers: [{ label: 'Plan price', value: '$49/mo' }], checklist: [{ text: 'Wire webhooks', done: false }], links: ['https://stripe.com/docs'] } } }

describe('labels', () => {
  it('shows the richer fields and saves only the corrected labels', async () => {
    const { calls } = mockApi({
      'GET /admin/api/session': authenticatedSession, 'GET /admin/api/projects': { body: { projects: [atlas, beacon] } },
      'GET /admin/api/table/projects': { body: { projects: [], metadata: { total: 0, ready: 0, failed: 0, active: false } } },
      'GET /admin/api/entries': { body: { entries: [decision], sources: [], tags: [] } },
      'GET /admin/api/entries/60/related': { body: { related: [] } },
      'GET /admin/api/entries/60': { body: { ...decision, repeats: [], repeats_total: 0 } },
      'GET /admin/api/entries/60/history': { body: { history: [] } },
      'POST /admin/api/entries/60/labels': { body: { saved: true } },
    })
    renderApp('/admin/table?view=decisions')
    const user = userEvent.setup()
    expect(await screen.findByText('Check')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Use Stripe' }))
    for (const text of ['Stripe', 'Paddle', 'Plan price: $49/mo', 'Wire webhooks', 'https://stripe.com/docs']) expect(screen.getAllByText(text, { exact: false }).length).toBeGreaterThan(0)
    await user.click(screen.getByRole('button', { name: 'Edit labels' }))
    await user.selectOptions(screen.getByRole('combobox', { name: /importance/i }), 'important')
    await user.clear(screen.getByRole('textbox', { name: /tags/i }))
    await user.type(screen.getByRole('textbox', { name: /tags/i }), 'billing, stripe')
    await user.click(screen.getByRole('button', { name: 'Save labels' }))
    expect(await screen.findByText(/Labels saved/)).toBeInTheDocument()
    const save = calls.find((call) => call.path === '/admin/api/entries/60/labels')
    expect(save?.body).toEqual({ set: { importance: 'important', tags: ['billing', 'stripe'] }, reset: [] })
  })
})
