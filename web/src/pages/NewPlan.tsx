import { useState } from 'react'
import { ArrowLeft, ArrowRight, CalendarDays, MapPin, UsersRound } from 'lucide-react'
import { type Plan, post } from '../api'

const categories = ['Badminton', 'Board games', 'Study', 'Food', 'Other']
function localValue(date: Date) { const offset = date.getTimezoneOffset() * 60000; return new Date(date.getTime() - offset).toISOString().slice(0, 16) }
export function NewPlan({ navigate }: { navigate: (to: string) => void }) {
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [category, setCategory] = useState('Badminton')
  const [area, setArea] = useState('')
  const [location, setLocation] = useState('')
  const [startsAt, setStartsAt] = useState(localValue(new Date(Date.now() + 3 * 3600000)))
  const [deadline, setDeadline] = useState(localValue(new Date(Date.now() + 2 * 3600000)))
  const [spaces, setSpaces] = useState(1)
  const [tags, setTags] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function submit(e: React.FormEvent) {
    e.preventDefault(); setError(''); setBusy(true)
    try {
      const plan = await post<Plan>('/api/plans', { title, description, category, area, location, startsAt: new Date(startsAt).toISOString(), joinDeadline: new Date(deadline).toISOString(), spaces, tags: tags.split(',').map(t => t.trim()).filter(Boolean) }, { 'Idempotency-Key': crypto.randomUUID() })
      navigate(`/plans/${plan.id}`)
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  return <div className="content-wrap form-page"><button className="back-link" onClick={() => navigate('/')}><ArrowLeft size={17} /> Back to plans</button><div className="form-header"><span className="form-icon"><UsersRound size={25} /></span><h1>Start something good.</h1><p>A few details and you’re ready to invite one more.</p></div><form className="plan-form" onSubmit={submit}><div className="form-block"><h2>The plan</h2><label className="field"><span>What are you doing?</span><input autoFocus required minLength={3} maxLength={80} value={title} onChange={e => setTitle(e.target.value)} placeholder="Badminton after class" /></label><div className="field-grid"><label className="field"><span>Activity</span><select value={category} onChange={e => setCategory(e.target.value)}>{categories.map(c => <option key={c}>{c}</option>)}</select></label><label className="field"><span>Spaces for others</span><input type="number" min="1" max="20" value={spaces} onChange={e => setSpaces(Number(e.target.value))} required /></label></div><label className="field"><span>A little more detail <em>optional</em></span><textarea maxLength={500} rows={3} value={description} onChange={e => setDescription(e.target.value)} placeholder="Anything people should know before joining?" /></label><label className="field"><span>Tags <em>optional, comma separated</em></span><input value={tags} onChange={e => setTags(e.target.value)} placeholder="casual, beginner friendly" /></label></div><div className="form-block"><h2><MapPin size={19} /> Where</h2><div className="field-grid"><label className="field"><span>Area</span><input required maxLength={60} value={area} onChange={e => setArea(e.target.value)} placeholder="North campus" /></label><label className="field"><span>Meeting place</span><input required maxLength={120} value={location} onChange={e => setLocation(e.target.value)} placeholder="Sports hall, court 2" /></label></div></div><div className="form-block"><h2><CalendarDays size={19} /> When</h2><div className="field-grid"><label className="field"><span>Starts</span><input type="datetime-local" required value={startsAt} min={localValue(new Date(Date.now() + 5 * 60000))} onChange={e => setStartsAt(e.target.value)} /></label><label className="field"><span>Join by</span><input type="datetime-local" required value={deadline} max={startsAt} onChange={e => setDeadline(e.target.value)} /></label></div><p className="field-help">The join deadline must be before the start. Spaces exclude you, the host.</p></div>{error && <p className="inline-error" role="alert">{error}</p>}<div className="form-footer"><button className="secondary-button" type="button" onClick={() => navigate('/')}>Cancel</button><button className="primary-button" type="submit" disabled={busy}>{busy ? 'Creating…' : 'Create plan'} <ArrowRight size={17} /></button></div></form></div>
}
