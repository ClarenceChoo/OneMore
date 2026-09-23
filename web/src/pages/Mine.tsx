import { useEffect, useState } from 'react'
import { CalendarDays, Plus } from 'lucide-react'
import { type Plan, request } from '../api'
import { PlanCard } from '../components/PlanCard'

type Group = 'All' | 'Hosting' | 'Going' | 'Offers' | 'Waitlist'
const groups: Group[] = ['All', 'Hosting', 'Going', 'Offers', 'Waitlist']
export function Mine({ navigate, revision }: { navigate: (to: string) => void; revision: number }) {
  const [plans, setPlans] = useState<Plan[]>([])
  const [group, setGroup] = useState<Group>('All')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  useEffect(() => { request<Plan[]>('/api/my-plans').then(setPlans).catch((e: Error) => setError(e.message)).finally(() => setLoading(false)) }, [revision])
  const shown = plans.filter(p => group === 'All' || p.myStatus === ({ Hosting: 'host', Going: 'confirmed', Offers: 'offered', Waitlist: 'waitlisted' } as Record<string, string>)[group])
  return <div className="content-wrap standard-page"><div className="page-head"><div><h1>My plans</h1><p>Everything you’re part of, all in one place.</p></div><button className="primary-button desktop-action" onClick={() => navigate('/new')}><Plus size={18} /> New plan</button></div><div className="tab-bar" role="tablist" aria-label="Plan status">{groups.map(g => <button key={g} role="tab" aria-selected={group === g} className={group === g ? 'selected' : ''} onClick={() => setGroup(g)}>{g}</button>)}</div>{error && <p className="inline-error" role="alert">{error}</p>}{loading ? <div className="list-loading">Loading your plans…</div> : shown.length ? <div className="plan-list">{shown.map(plan => <PlanCard key={plan.id} plan={plan} onClick={() => navigate(`/plans/${plan.id}`)} />)}</div> : <div className="empty-state"><div className="empty-icon"><CalendarDays size={24} /></div><h3>{group === 'All' ? 'Nothing planned yet' : `No ${group.toLowerCase()} plans`}</h3><p>Find something to join, or bring your own idea to life.</p><button className="primary-button" onClick={() => navigate('/')}>Explore plans</button></div>}</div>
}
