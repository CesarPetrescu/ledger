import { screen, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { authenticatedSession, mockApi, renderApp } from './helpers'

it('covers the calendar, research, repos, and where access settings live', async () => {
  mockApi({ 'GET /admin/api/session': authenticatedSession })
  renderApp('/admin/help')
  const where = await screen.findByRole('region', { name: 'Where to look' })
  expect(within(where).getByRole('link', { name: 'Calendar' })).toHaveAttribute('href', '/admin/calendar')

  const research = screen.getByRole('region', { name: 'Research' })
  expect(within(research).getByRole('link', { name: 'Handoffs' })).toHaveAttribute('href', '/admin/handoffs')
  for (const step of ['Accept', 'Send back', 'Resume', 'Share with research runs']) expect(within(research).getByText(step)).toBeInTheDocument()
  expect(research).toHaveTextContent(/files travel both ways/i)

  const access = screen.getByRole('region', { name: 'Repos and access' })
  for (const topic of ['Repos', 'GitHub sync', 'API keys']) expect(within(access).getByText(topic)).toBeInTheDocument()
  expect(within(access).getByRole('link', { name: 'Agents' })).toHaveAttribute('href', '/admin/agents')
})
