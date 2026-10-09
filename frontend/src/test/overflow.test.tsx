import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { OverflowMenu } from '../components/OverflowMenu'
import { ResearchStatusBadge, researchStatusLabel } from '../components/ui'

describe('overflow menu', () => {
  it('opens on click, focuses the first item, runs it, and closes on Escape or outside clicks', async () => {
    const remove = vi.fn()
    render(<div><OverflowMenu label="More actions for Atlas" items={[{ label: 'Delete project', onSelect: remove, danger: true }]} /><p>outside</p></div>)
    const user = userEvent.setup()
    const toggle = screen.getByRole('button', { name: 'More actions for Atlas' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await user.click(toggle)
    expect(screen.getByRole('menuitem', { name: 'Delete project' })).toHaveFocus()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    expect(toggle).toHaveFocus()
    await user.click(toggle)
    await user.click(screen.getByText('outside'))
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    await user.click(toggle)
    await user.click(screen.getByRole('menuitem', { name: 'Delete project' }))
    expect(remove).toHaveBeenCalledOnce()
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('closes when keyboard focus leaves it, so it never covers what has focus', async () => {
    render(<div><OverflowMenu label="More actions for Atlas" items={[{ label: 'Delete project', onSelect: vi.fn(), danger: true }]} /><button type="button">next</button></div>)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'More actions for Atlas' }))
    await user.tab()
    expect(screen.getByRole('button', { name: 'next' })).toHaveFocus()
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })

  it('hands focus back to its button before running an item, so a dialog the item opens returns focus there', async () => {
    let focused: Element | null = null
    render(<OverflowMenu label="More actions for this entry" items={[{ label: 'Delete entry', onSelect: () => { focused = document.activeElement }, danger: true }]} />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'More actions for this entry' }))
    await user.click(screen.getByRole('menuitem', { name: 'Delete entry' }))
    expect(focused).toBe(screen.getByRole('button', { name: 'More actions for this entry' }))
  })

  it('names every research state the same way', () => {
    expect(researchStatusLabel('review')).toBe('Ready for review')
    expect(researchStatusLabel('question')).toBe('Question for you')
    render(<ResearchStatusBadge status="accepted" />)
    expect(screen.getByText('Accepted')).toHaveAttribute('data-research', 'accepted')
  })
})
