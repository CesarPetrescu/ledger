import { EntryDetail } from '../components/EntryPanel'

/** One entry on its own page: links from search, history, and related entries land here. */
export function EntryPage({ id }: { id: string }) {
  return (
    <div className="entry-page">
      <EntryDetail id={id} />
    </div>
  )
}
