import { AiStatus, InboxView } from '../components/entries'
import { LegendButton } from '../components/help'

/** The home page: what your agents are waiting on, then what's due, then the week. */
export function InboxPage() {
  return (
    <>
      <header className="page-head">
        <div>
          <p className="eyebrow">What needs you</p>
          <h1>Inbox</h1>
        </div>
        <LegendButton />
        <p className="muted">Questions your agents are waiting on you to answer, the todos that are due or most urgent, and how each project's week went.</p>
        <AiStatus />
      </header>
      <InboxView />
    </>
  )
}
