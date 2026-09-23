import { useCallback, useEffect, useState } from 'react'
import { Bell, Compass, LogOut, Plus, UsersRound } from 'lucide-react'
import { ApiError, type AuthConfig, type User, request } from './api'
import { Feed } from './pages/Feed'
import { Detail } from './pages/Detail'
import { NewPlan } from './pages/NewPlan'
import { Mine } from './pages/Mine'
import { Inbox } from './pages/Inbox'

type Path = string
function currentPath() { return window.location.pathname }

export default function App() {
  const [path, setPath] = useState<Path>(currentPath)
  const [user, setUser] = useState<User | null>(null)
  const [auth, setAuth] = useState<AuthConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const navigate = useCallback((to: string) => { window.history.pushState({}, '', to); setPath(to); window.scrollTo(0, 0) }, [])
  useEffect(() => { const onPop = () => setPath(currentPath()); window.addEventListener('popstate', onPop); return () => window.removeEventListener('popstate', onPop) }, [])
  useEffect(() => {
    Promise.all([request<AuthConfig>('/auth/config'), request<User>('/api/me').catch((e: unknown) => { if (e instanceof ApiError && e.status === 401) return null; throw e })])
      .then(([config, me]) => { setAuth(config); setUser(me) }).catch((e: Error) => setError(e.message)).finally(() => setLoading(false))
  }, [])
  useEffect(() => {
    if (!user) return
    const source = new EventSource('/api/events')
    source.addEventListener('refresh', () => setRevision(n => n + 1))
    return () => source.close()
  }, [user])
  async function logout() { await request('/auth/logout', { method: 'POST' }); setUser(null); navigate('/') }
  if (loading) return <div className="loading-screen"><span className="brand-mark"><i /><i /><i /></span><p>Opening OneMore…</p></div>
  if (!user) return <Welcome auth={auth} error={error} onLogin={setUser} />
  const active = path.startsWith('/plans/') || path === '/' ? 'discover' : path === '/mine' ? 'mine' : path === '/inbox' ? 'inbox' : 'new'
  return <div className="app-shell">
    <aside className="sidebar">
      <button className="brand" onClick={() => navigate('/')} aria-label="OneMore home"><span className="brand-mark"><i /><i /><i /></span><span>OneMore</span></button>
      <nav className="side-nav" aria-label="Main navigation">
        <NavItem icon={<Compass size={19} />} label="Discover" active={active === 'discover'} onClick={() => navigate('/')} />
        <NavItem icon={<UsersRound size={19} />} label="My plans" active={active === 'mine'} onClick={() => navigate('/mine')} />
        <NavItem icon={<Bell size={19} />} label="Inbox" active={active === 'inbox'} onClick={() => navigate('/inbox')} />
      </nav>
      <button className="sidebar-create" onClick={() => navigate('/new')}><Plus size={18} /> Create a plan</button>
      <div className="sidebar-bottom"><div className="avatar">{user.name.charAt(0)}</div><div className="profile-copy"><strong>{user.name}</strong><small>Your space</small></div><button className="icon-button" aria-label="Sign out" title="Sign out" onClick={logout}><LogOut size={18} /></button></div>
    </aside>
    <div className="mobile-header"><button className="brand" onClick={() => navigate('/')}><span className="brand-mark"><i /><i /><i /></span><span>OneMore</span></button><button className="mobile-create" onClick={() => navigate('/new')}><Plus size={19} /><span>New plan</span></button></div>
    <main className="main-content">
      {path === '/new' ? <NewPlan navigate={navigate} /> : path === '/mine' ? <Mine navigate={navigate} revision={revision} /> : path === '/inbox' ? <Inbox navigate={navigate} revision={revision} /> : path.startsWith('/plans/') ? <Detail id={path.split('/')[2]} navigate={navigate} revision={revision} /> : <Feed navigate={navigate} revision={revision} />}
    </main>
    <nav className="mobile-nav" aria-label="Main navigation">
      <NavItem icon={<Compass size={20} />} label="Discover" active={active === 'discover'} onClick={() => navigate('/')} />
      <NavItem icon={<UsersRound size={20} />} label="My plans" active={active === 'mine'} onClick={() => navigate('/mine')} />
      <NavItem icon={<Bell size={20} />} label="Inbox" active={active === 'inbox'} onClick={() => navigate('/inbox')} />
    </nav>
  </div>
}

function NavItem({ icon, label, active, onClick }: { icon: React.ReactNode; label: string; active: boolean; onClick: () => void }) {
  return <button className={`nav-item ${active ? 'active' : ''}`} aria-current={active ? 'page' : undefined} onClick={onClick}>{icon}<span>{label}</span></button>
}

function Welcome({ auth, error, onLogin }: { auth: AuthConfig | null; error: string; onLogin: (u: User) => void }) {
  const [busy, setBusy] = useState('')
  const [localError, setLocalError] = useState(error)
  async function demo(name: string) { setBusy(name); setLocalError(''); try { await request('/auth/dev', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }) }); onLogin(await request<User>('/api/me')) } catch (e) { setLocalError((e as Error).message) } finally { setBusy('') } }
  return <div className="welcome"><div className="welcome-top"><span className="brand"><span className="brand-mark"><i /><i /><i /></span><span>OneMore</span></span></div><div className="welcome-main"><div className="welcome-illustration" aria-hidden="true"><div className="orbit orbit-one" /><div className="orbit orbit-two" /><div className="face face-one">A</div><div className="face face-two">S</div><div className="face face-three">J</div><div className="empty-seat"><Plus size={29} /></div></div><div className="welcome-copy"><h1>Good plans are<br />better together.</h1><p>Find the extra person your plan needs, or join someone else’s. Simple plans, clear spots, no group chat chase.</p>{auth?.oidc && <a className="primary-button" href="/auth/login">Sign in to get started</a>}{auth?.devAuth && <div className="demo-login"><span>Choose a demo profile</span><div>{['Alex', 'Sam', 'Jordan'].map(name => <button key={name} disabled={!!busy} onClick={() => demo(name)}>{busy === name ? 'Opening…' : name}</button>)}</div></div>}{!auth?.oidc && !auth?.devAuth && <p className="inline-error">Sign-in is not configured.</p>}{localError && <p className="inline-error" role="alert">{localError}</p>}</div></div></div>
}
