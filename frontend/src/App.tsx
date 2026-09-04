import { AuthProvider, useAuth } from './auth'
import { Shell } from './components/Shell'
import { ToastProvider } from './components/Toast'
import { Link, useLocation } from './router'
import { ClientsPage } from './pages/ClientsPage'
import { LoginPage } from './pages/LoginPage'
import { OverviewPage } from './pages/OverviewPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { SearchPage } from './pages/SearchPage'
import { CalendarPage } from './pages/CalendarPage'
import { HandoffsPage } from './pages/HandoffsPage'

function resolve(path: string, query: URLSearchParams): { title: string; page: React.ReactNode } {
  if (path === '/') return { title: 'Overview', page: <OverviewPage /> }
  if (path === '/projects') return { title: 'Projects', page: <ProjectsPage /> }
  const project = /^\/projects\/([^/]+)$/.exec(path)
  if (project?.[1]) return { title: 'Projects', page: <ProjectsPage slug={decodeURIComponent(project[1])} /> }
  const projectView = /^\/projects\/([^/]+)\/(handoffs|files)$/.exec(path)
  if (projectView?.[1] && projectView[2]) return { title: 'Projects', page: <ProjectsPage slug={decodeURIComponent(projectView[1])} view={projectView[2] as 'handoffs' | 'files'} /> }
  if (path === '/search') return { title: 'Search', page: <SearchPage /> }
  if (path === '/calendar') return { title: 'Calendar', page: <CalendarPage /> }
  if (path === '/handoffs') return { title: 'Handoffs', page: <HandoffsPage /> }
  if (path === '/handoffs/new') return { title: 'New handoff', page: <HandoffsPage creating initialProject={query.get('project') ?? ''} /> }
  const handoff = /^\/handoffs\/([^/]+)$/.exec(path)
  if (handoff?.[1]) return { title: 'Handoffs', page: <HandoffsPage id={decodeURIComponent(handoff[1])} /> }
  if (path === '/clients') return { title: 'OAuth clients', page: <ClientsPage /> }
  return {
    title: 'Not found',
    page: (
      <div className="state state-empty">
        <p>There is nothing at this address.</p>
        <Link to="/" className="btn">
          Back to overview
        </Link>
      </div>
    ),
  }
}

function Root() {
  const { state } = useAuth()
  const { path, query } = useLocation()
  if (state.status === 'loading') {
    return (
      <p className="splash muted" aria-busy="true">
        Checking session…
      </p>
    )
  }
  if (state.status === 'anonymous') {
    return <LoginPage notice={state.notice} />
  }
  const { title, page } = resolve(path, query)
  return <Shell title={title}>{page}</Shell>
}

export default function App() {
  return (
    <ToastProvider>
      <AuthProvider>
        <Root />
      </AuthProvider>
    </ToastProvider>
  )
}
