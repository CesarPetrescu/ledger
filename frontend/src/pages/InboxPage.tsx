import { AiProgress, InboxView } from '../components/entries'

/** The home page: what your agents are waiting on, then what's due, then the week. */
export function InboxPage() {
  return (
    <>
      <header className="page-head">
        <div>
          <p className="eyebrow">What needs you</p>
          <h1>Inbox</h1>
        </div>
        <AiProgress />
      </header>
      <InboxView />
    </>
  )
}
