import { LabelLegend } from '../components/help'
import { Link } from '../router'

/** How Ledger works and how it decides what to show, in one short page. */
export function HelpPage() {
  return (
    <article className="help-page">
      <header className="page-head">
        <div>
          <p className="eyebrow">How Ledger works</p>
          <h1>Help</h1>
        </div>
        <p className="muted">Your agents write down what they did, decided, and need from you. Ledger keeps all of it, labels it with AI, and shows you what matters first.</p>
      </header>

      <section aria-labelledby="help-where">
        <h2 id="help-where" className="section-title">Where to look</h2>
        <dl className="help-list">
          <div><dt><Link to="/">Inbox</Link></dt><dd>Start here. Questions agents are waiting on you to answer, the most urgent todos, blocked projects, and each project's week.</dd></div>
          <div><dt><Link to="/projects">Projects</Link></dt><dd>One page per project: its weekly summary and health, then its activity, todos, decisions, details, handoffs, files, and repos.</dd></div>
          <div><dt><Link to="/table">Table</Link></dt><dd>Every project's entries in one list, filterable by project, agent, tag, or text, with a spreadsheet download. Reading collects linked articles.</dd></div>
          <div><dt><Link to="/calendar">Calendar</Link></dt><dd>Todos that are due, project deadlines, and the day snoozed items come back. Connect Nextcloud to see your own events too; agents see only the calendars you select.</dd></div>
          <div><dt><Link to="/handoffs">Handoffs</Link></dt><dd>Work passed from one agent, or from you, to another, as a thread per task. Research tasks live here too.</dd></div>
          <div><dt><Link to="/agents">Agents</Link></dt><dd>What each agent did lately, how to connect a new one, and the access settings.</dd></div>
          <div><dt><Link to="/search">Search</Link></dt><dd>Finds entries and projects by meaning as well as exact words.</dd></div>
        </dl>
      </section>

      <section aria-labelledby="help-inbox">
        <h2 id="help-inbox" className="section-title">How the Inbox decides</h2>
        <ul className="help-points">
          <li><strong>Needs you</strong> lists entries where the AI found a question or request for you. Each stays until you mark it handled; a snoozed one comes back the next day.</li>
          <li><strong>Todos</strong> shows open todos, the most urgent first: due within a week, then high priority, then the oldest.</li>
          <li><strong>Blocked</strong> lists projects whose latest status update says they are blocked, with the blocker.</li>
          <li><strong>This week</strong> is an AI summary of each active project's last seven days, refreshed at least daily.</li>
        </ul>
      </section>

      <section aria-labelledby="help-labels">
        <h2 id="help-labels" className="section-title">AI labels</h2>
        <ul className="help-points">
          <li>Titles, summaries, tags, and the labels below are written by AI from each entry's text. The text itself is never changed.</li>
          <li>Open an entry and choose <strong>Edit labels</strong> to correct anything. Your corrections are kept when the AI labels the entry again, and it learns from them for similar entries. <strong>Reset to AI</strong> brings its reading back.</li>
          <li>Routine entries, such as checkpoints and heartbeats, are hidden from lists unless you tick <strong>Show routine entries</strong> or search. Repeats of the same news are folded under one row.</li>
        </ul>
        <LabelLegend />
      </section>

      <section aria-labelledby="help-research">
        <h2 id="help-research" className="section-title">Research</h2>
        <ul className="help-points">
          <li>Choose <strong>Research</strong> on <Link to="/handoffs">Handoffs</Link> to queue a task: what to find out, the checks the result must meet, and any files it needs. A sandbox picks it up and works until every check is met. Agents can queue research too.</li>
          <li>Its badge shows where it stands: Draft, Queued, Running, Ready for review, Question for you, Stopped, or Accepted.</li>
          <li>When it is ready for review, read the result, then <strong>Accept</strong> it, or reply with what to change and <strong>Send back</strong>. Accepting publishes the result to the project's log. A question for you is answered the same way: reply, then <strong>Resume</strong>.</li>
          <li>Files travel both ways: attach them to the task or to a reply, and the run's result files appear in the thread.</li>
          <li>Runs browse the open web. They see a project's details and repos only if you turn on <strong>Share with research runs</strong> on its Details tab.</li>
        </ul>
      </section>

      <section aria-labelledby="help-access">
        <h2 id="help-access" className="section-title">Repos and access</h2>
        <ul className="help-points">
          <li>A project's <strong>Repos</strong> tab lists the Git repositories it spans, so agents know where its code lives. Agents clone with their own Git access; a link never carries a password or token.</li>
          <li><strong>GitHub sync</strong> uses a read-only GitHub token to refresh each linked GitHub repository every 15 minutes: its latest commit, open pull requests, and latest release. Turning it off forgets the token and what it synced; the links stay.</li>
          <li><strong>API keys</strong> let a server, such as a research dispatcher, use Ledger without signing in. A key is shown once and can only dispatch research.</li>
          <li>Access settings are on <Link to="/agents">Agents</Link>: connected apps (revoke one to cut its access at once), API keys, GitHub sync, and the approval password.</li>
        </ul>
      </section>

      <section aria-labelledby="help-undo">
        <h2 id="help-undo" className="section-title">Undo and Trash</h2>
        <ul className="help-points">
          <li>Every quick action (mark done, reopen, read, star, handled, snooze, delete) shows an Undo button, and you can undo any of the last seven days' actions from <Link to="/table?view=recent">Recent actions</Link>.</li>
          <li>Delete an entry or a project from its ⋯ menu; it moves to <Link to="/table?view=trash">Trash</Link>, where it can be restored for 30 days. Entries can't be edited; add a correction as a new entry.</li>
        </ul>
      </section>
    </article>
  )
}
