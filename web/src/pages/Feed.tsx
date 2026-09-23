import { useEffect, useRef, useState } from 'react'
import { ArrowRight, Compass, SlidersHorizontal, Sparkles, UsersRound } from 'lucide-react'
import { type DiscoveryPreferences, type FeedPage, type Plan, post, request } from '../api'
import { PlanCard } from '../components/PlanCard'

const categories = ['All activities', 'Badminton', 'Board games', 'Study', 'Food', 'Other']
const initialPreferences = (): DiscoveryPreferences => ({ categories: [], area: '', availability: 'any', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC' })
type Mode = 'soonest' | 'for_you'

export function Feed({ navigate, revision }: { navigate: (to: string) => void; revision: number }) {
  const [mode, setMode] = useState<Mode>('soonest')
  const [category, setCategory] = useState('')
  const [area, setArea] = useState('')
  const [period, setPeriod] = useState('all')
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState<FeedPage>({ plans: [] })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [more, setMore] = useState(false)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<DiscoveryPreferences>(initialPreferences)
  const [preferenceError, setPreferenceError] = useState('')
  const [saving, setSaving] = useState(false)
  const [preferenceRevision, setPreferenceRevision] = useState(0)
  const seen = useRef(new Set<string>())

  useEffect(() => { request<DiscoveryPreferences>('/api/preferences').then(setDraft).catch(() => {}) }, [])

  async function load(cursor = '') {
    if (cursor) setMore(true); else setLoading(true)
    setError('')
    try {
      if (mode === 'for_you') {
        setPage(await request<FeedPage>('/api/recommendations'))
      } else {
        const params = new URLSearchParams()
        if (category) params.set('category', category)
        if (area.trim()) params.set('area', area.trim())
        if (open) params.set('open', 'true')
        if (period !== 'all') {
          const before = new Date()
          before.setHours(23, 59, 59, 999)
          if (period === 'week') before.setDate(before.getDate() + 7)
          params.set('before', before.toISOString())
        }
        if (cursor) params.set('cursor', cursor)
        const data = await request<FeedPage>(`/api/plans?${params}`)
        setPage(old => cursor ? { plans: [...old.plans, ...data.plans], nextCursor: data.nextCursor } : data)
      }
    } catch (e) { setError((e as Error).message) } finally { setLoading(false); setMore(false) }
  }
  useEffect(() => { load() }, [mode, category, area, period, open, revision, preferenceRevision])

  useEffect(() => {
    if (loading || !page.plans.length) return
    const observer = new IntersectionObserver(entries => {
      const events = entries.filter(entry => entry.isIntersecting).map(entry => {
        observer.unobserve(entry.target)
        const planId = entry.target.getAttribute('data-discovery-id') || ''
        const key = `${mode}:${planId}`
        if (!planId || seen.current.has(key)) return null
        seen.current.add(key)
        return { planId, kind: 'impression', source: mode }
      }).filter((event): event is { planId: string; kind: string; source: Mode } => event !== null)
      if (events.length) void post('/api/discovery/events', { events }).catch(() => {})
    }, { threshold: 0.5 })
    document.querySelectorAll('[data-discovery-id]').forEach(element => observer.observe(element))
    return () => observer.disconnect()
  }, [loading, page.plans, mode])

  function openPlan(plan: Plan) {
    void post('/api/discovery/events', { events: [{ planId: plan.id, kind: 'click', source: mode }] }).catch(() => {})
    navigate(`/plans/${plan.id}`)
  }
  function toggleInterest(value: string) {
    setDraft(current => ({ ...current, categories: current.categories.includes(value) ? current.categories.filter(item => item !== value) : [...current.categories, value] }))
  }
  async function savePreferences(event: React.FormEvent) {
    event.preventDefault()
    setSaving(true)
    setPreferenceError('')
    try {
      const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
      setDraft(await post<DiscoveryPreferences>('/api/preferences', { ...draft, timezone }))
      setEditing(false)
      setPreferenceRevision(value => value + 1)
    } catch (e) { setPreferenceError((e as Error).message) } finally { setSaving(false) }
  }

  return <div className="content-wrap feed-page">
    <section className="feed-hero"><div><div className="hero-kicker"><Sparkles size={15} /> Plans worth showing up for</div><h1>Make room for<br /><span>one more.</span></h1><p>Find a plan nearby, or start one and see who’s in.</p><button className="text-link" onClick={() => navigate('/new')}>Start a plan <ArrowRight size={17} /></button></div><div className="hero-art" aria-hidden="true"><span className="art-circle art-circle-one" /><span className="art-circle art-circle-two" /><span className="art-person art-a"><UsersRound size={35} /></span><span className="art-person art-b"><Compass size={43} /></span><span className="art-person art-c">+</span><span className="art-caption">There’s space for you.</span></div></section>
    <section className="feed-section">
      <div className="section-heading"><div><h2>Upcoming plans</h2><p>{mode === 'soonest' ? 'Start with what’s happening soon.' : 'Ranked by your interests, area, time, and activity.'}</p></div><span className="section-count">{page.plans.length} shown</span></div>
      <div className="feed-mode-row"><div className="tab-bar feed-tabs" aria-label="Discover plans"><button className={mode === 'soonest' ? 'selected' : ''} onClick={() => setMode('soonest')}>Soonest</button><button className={mode === 'for_you' ? 'selected' : ''} onClick={() => setMode('for_you')}>For you</button></div>{mode === 'for_you' && <button className="preferences-toggle" onClick={() => setEditing(value => !value)}>{editing ? 'Close preferences' : 'Tune your feed'}</button>}</div>
      {mode === 'for_you' && editing && <form className="preferences-panel" onSubmit={savePreferences}><fieldset><legend>Interests</legend><div className="interest-options">{categories.slice(1).map(item => <label key={item}><input type="checkbox" checked={draft.categories.includes(item)} onChange={() => toggleInterest(item)} /> {item}</label>)}</div></fieldset><div className="preference-fields"><label className="field"><span>Preferred area</span><input value={draft.area} onChange={event => setDraft(current => ({ ...current, area: event.target.value }))} maxLength={60} placeholder="Any area" /></label><label className="field"><span>Availability</span><select value={draft.availability} onChange={event => setDraft(current => ({ ...current, availability: event.target.value as DiscoveryPreferences['availability'] }))}><option value="any">Any time</option><option value="weekday_evenings">Weekday evenings</option><option value="weekends">Weekends</option></select></label></div>{preferenceError && <p className="inline-error" role="alert">{preferenceError}</p>}<button className="primary-button" type="submit" disabled={saving}>{saving ? 'Saving…' : 'Save preferences'}</button></form>}
      {mode === 'soonest' && <div className="filters" aria-label="Filter plans"><span className="filter-icon"><SlidersHorizontal size={18} /></span><label><span className="sr-only">Activity</span><select value={category} onChange={event => setCategory(event.target.value)}>{categories.map((item, index) => <option key={item} value={index === 0 ? '' : item}>{item}</option>)}</select></label><label><span className="sr-only">Time</span><select value={period} onChange={event => setPeriod(event.target.value)}><option value="all">Any time</option><option value="today">Today</option><option value="week">Next 7 days</option></select></label><label><span className="sr-only">Area</span><input value={area} onChange={event => setArea(event.target.value)} placeholder="Area" /></label><label className="open-filter"><input type="checkbox" checked={open} onChange={event => setOpen(event.target.checked)} /> Open spaces</label></div>}
      {error && <p className="inline-error" role="alert">{error} <button onClick={() => load()}>Try again</button></p>}
      {loading ? <div className="list-loading">Finding plans…</div> : page.plans.length ? <><div className="plan-list">{page.plans.map(plan => <PlanCard key={plan.id} plan={plan} onClick={() => openPlan(plan)} />)}</div>{mode === 'soonest' && page.nextCursor && <button className="secondary-button load-more" disabled={more} onClick={() => load(page.nextCursor)}>{more ? 'Loading…' : 'Show more plans'}</button>}</> : <div className="empty-state"><div className="empty-icon"><Sparkles size={24} /></div><h3>No plans found yet</h3><p>{mode === 'for_you' ? 'Try adding interests or check back when new plans appear.' : 'Try another filter or start something yourself.'}</p><button className="primary-button" onClick={() => navigate('/new')}>Create a plan</button></div>}
    </section>
  </div>
}
