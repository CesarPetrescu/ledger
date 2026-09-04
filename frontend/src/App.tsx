import { AuthProvider, useAuth } from './auth'
import { Shell } from './components/Shell'
import { ToastProvider } from './components/Toast'
import { Link, useLocation } from './router'
import { ClientsPage } from './pages/ClientsPage'
import { LoginPage } from './pages/LoginPage'
import { OverviewPage } from './pages/OverviewPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { SearchPage } from './pages/SearchPage'

function resolve(path: string): { title: string; page: React.ReactNode } {
  if (path === '/') return { title: 'Overview', page: <OverviewPage /> }
  if (path === '/projects') return { title: 'Projects', page: <ProjectsPage /> }
  const project = /^\/projects\/([^/]+)$/.exec(path)
  if (project?.[1]) return { title: 'Projects', page: <ProjectsPage slug={decodeURIComponent(project[1])} /> }
  if (path === '/search') return { title: 'Search', page: <SearchPage /> }
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
  const { path } = useLocation()
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
  const { title, page } = resolve(path)
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
