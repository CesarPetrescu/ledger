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

  it('moves between items with the arrow keys, Home, and End, and keeps those keys from the page behind', async () => {
    const pageKeys = vi.fn()
    const onWindowKey = (event: KeyboardEvent) => { if (!event.defaultPrevented) pageKeys(event.key) }
    window.addEventListener('keydown', onWindowKey)
    try {
      render(<OverflowMenu label="More actions" items={[{ label: 'Copy link', onSelect: vi.fn() }, { label: 'Archived', onSelect: vi.fn(), disabled: true }, { label: 'Delete', onSelect: vi.fn(), danger: true }]} />)
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'More actions' }))
      expect(screen.getByRole('menuitem', { name: 'Copy link' })).toHaveFocus()
      await user.keyboard('{ArrowDown}')
      expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveFocus()
      await user.keyboard('{ArrowDown}')
      expect(screen.getByRole('menuitem', { name: 'Copy link' })).toHaveFocus()
      await user.keyboard('{ArrowUp}')
      expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveFocus()
      await user.keyboard('{Home}')
      expect(screen.getByRole('menuitem', { name: 'Copy link' })).toHaveFocus()
      await user.keyboard('{End}')
      expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveFocus()
      expect(pageKeys).not.toHaveBeenCalled()
    } finally {
      window.removeEventListener('keydown', onWindowKey)
    }
  })

  it('opens rightward from a button near the screen’s left edge, where opening leftward would cut it off', async () => {
    const at = (left: number) => ({ left, right: left + 180, top: 0, bottom: 120, width: 180, height: 120, x: left, y: 0, toJSON: () => ({}) }) as DOMRect
    const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    try {
      render(<OverflowMenu label="Snooze" text="Snooze" items={[{ label: 'Until tomorrow', onSelect: vi.fn() }]} />)
      const user = userEvent.setup()
      rect.mockReturnValue(at(200))
      await user.click(screen.getByRole('button', { name: 'Snooze' }))
      expect(screen.getByRole('menu')).not.toHaveAttribute('data-align')
      await user.keyboard('{Escape}')
      // A phone row's actions start at the left: anchored to the button's right, the list would begin off screen.
      rect.mockReturnValue(at(-3))
      await user.click(screen.getByRole('button', { name: 'Snooze' }))
      expect(screen.getByRole('menu')).toHaveAttribute('data-align', 'start')
    } finally {
      rect.mockRestore()
    }
  })

  it('scrolls a list opened low on the page into view, so its last item is not left under the fold or the tab bar', async () => {
    const scrolled: Element[] = []
    Element.prototype.scrollIntoView = function (this: Element) { scrolled.push(this) }
    try {
      render(<OverflowMenu label="Snooze" text="Snooze" items={[{ label: 'Until tomorrow', onSelect: vi.fn() }, { label: 'For a week', onSelect: vi.fn() }]} />)
      await userEvent.setup().click(screen.getByRole('button', { name: 'Snooze' }))
      expect(scrolled).toEqual([screen.getByRole('menu')])
    } finally {
      delete (Element.prototype as Partial<Element>).scrollIntoView
    }
  })

  it('opens upward when it would end below the window and there is no page left to scroll', async () => {
    const at = (top: number) => ({ left: 200, right: 380, top, bottom: top + 130, width: 180, height: 130, x: 200, y: top, toJSON: () => ({}) }) as DOMRect
    const rect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect')
    try {
      render(<OverflowMenu label="Snooze" text="Snooze" items={[{ label: 'Until tomorrow', onSelect: vi.fn() }]} />)
      const user = userEvent.setup()
      rect.mockReturnValue(at(100))
      await user.click(screen.getByRole('button', { name: 'Snooze' }))
      expect(screen.getByRole('menu')).not.toHaveAttribute('data-side')
      await user.keyboard('{Escape}')
      // The last row on a phone: below it is only the tab bar.
      rect.mockReturnValue(at(window.innerHeight - 60))
      await user.click(screen.getByRole('button', { name: 'Snooze' }))
      expect(screen.getByRole('menu')).toHaveAttribute('data-side', 'top')
    } finally {
      rect.mockRestore()
    }
  })

  it('names every research state the same way', () => {
    expect(researchStatusLabel('review')).toBe('Ready for review')
    expect(researchStatusLabel('question')).toBe('Question for you')
    render(<ResearchStatusBadge status="accepted" />)
    expect(screen.getByText('Accepted')).toHaveAttribute('data-research', 'accepted')
  })
})
