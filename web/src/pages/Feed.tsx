import { useEffect, useState } from 'react'
import { ArrowRight, Compass, SlidersHorizontal, Sparkles, UsersRound } from 'lucide-react'
import { type FeedPage, request } from '../api'
import { PlanCard } from '../components/PlanCard'

const categories = ['All activities', 'Badminton', 'Board games', 'Study', 'Food', 'Other']
export function Feed({ navigate, revision }: { navigate: (to: string) => void; revision: number }) {
  const [category, setCategory] = useState('')
  const [area, setArea] = useState('')
  const [period, setPeriod] = useState('all')
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState<FeedPage>({ plans: [] })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [more, setMore] = useState(false)
  async function load(cursor = '') {
    if (cursor) setMore(true); else setLoading(true)
    setError('')
    const params = new URLSearchParams()
    if (category) params.set('category', category)
    if (area.trim()) params.set('area', area.trim())
    if (open) params.set('open', 'true')
    if (period !== 'all') { const before = new Date(); before.setHours(period === 'today' ? 23 : 23, 59, 59, 999); if (period === 'week') before.setDate(before.getDate() + 7); params.set('before', before.toISOString()) }
    if (cursor) params.set('cursor', cursor)
    try { const data = await request<FeedPage>(`/api/plans?${params}`); setPage(old => cursor ? { plans: [...old.plans, ...data.plans], nextCursor: data.nextCursor } : data) } catch (e) { setError((e as Error).message) } finally { setLoading(false); setMore(false) }
  }
  useEffect(() => { load() }, [category, area, period, open, revision])
  return <div className="content-wrap feed-page"><section className="feed-hero"><div><div className="hero-kicker"><Sparkles size={15} /> Plans worth showing up for</div><h1>Make room for<br /><span>one more.</span></h1><p>Find a plan nearby, or start one and see who’s in.</p><button className="text-link" onClick={() => navigate('/new')}>Start a plan <ArrowRight size={17} /></button></div><div className="hero-art" aria-hidden="true"><span className="art-circle art-circle-one" /><span className="art-circle art-circle-two" /><span className="art-person art-a"><UsersRound size={35} /></span><span className="art-person art-b"><Compass size={43} /></span><span className="art-person art-c">+</span><span className="art-caption">There’s space for you.</span></div></section>
    <section className="feed-section"><div className="section-heading"><div><h2>Upcoming plans</h2><p>Start with what’s happening soon.</p></div><span className="section-count">{page.plans.length} shown</span></div>
      <div className="filters" aria-label="Filter plans"><span className="filter-icon"><SlidersHorizontal size={18} /></span><label><span className="sr-only">Activity</span><select value={category} onChange={e => setCategory(e.target.value)}>{categories.map((item, i) => <option key={item} value={i === 0 ? '' : item}>{item}</option>)}</select></label><label><span className="sr-only">Time</span><select value={period} onChange={e => setPeriod(e.target.value)}><option value="all">Any time</option><option value="today">Today</option><option value="week">Next 7 days</option></select></label><label><span className="sr-only">Area</span><input value={area} onChange={e => setArea(e.target.value)} placeholder="Area" /></label><label className="open-filter"><input type="checkbox" checked={open} onChange={e => setOpen(e.target.checked)} /> Open spaces</label></div>
      {error && <p className="inline-error" role="alert">{error} <button onClick={() => load()}>Try again</button></p>}
      {loading ? <div className="list-loading">Finding plans…</div> : page.plans.length ? <><div className="plan-list">{page.plans.map(plan => <PlanCard key={plan.id} plan={plan} onClick={() => navigate(`/plans/${plan.id}`)} />)}</div>{page.nextCursor && <button className="secondary-button load-more" disabled={more} onClick={() => load(page.nextCursor)}>{more ? 'Loading…' : 'Show more plans'}</button>}</> : <div className="empty-state"><div className="empty-icon"><Sparkles size={24} /></div><h3>No plans found yet</h3><p>Try another filter or start something yourself.</p><button className="primary-button" onClick={() => navigate('/new')}>Create a plan</button></div>}
    </section></div>
}
