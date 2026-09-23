import { ArrowUpRight, MapPin, UsersRound } from 'lucide-react'
import { type Plan, dateLabel } from '../api'

const colors: Record<string, string> = { Badminton: 'blue', 'Board games': 'purple', Study: 'teal', Food: 'orange', Other: 'gray' }
export function statusText(plan: Plan) {
  if (plan.status === 'cancelled') return 'Cancelled'
  if (plan.myStatus === 'host') return 'Hosting'
  if (plan.myStatus === 'confirmed') return 'Going'
  if (plan.myStatus === 'waitlisted') return 'On waitlist'
  if (plan.myStatus === 'offered') return 'Offer waiting'
  if (new Date(plan.joinDeadline).getTime() <= Date.now()) return 'Closed'
  if (plan.available > 0) return `${plan.available} ${plan.available === 1 ? 'space' : 'spaces'} left`
  return 'Waitlist open'
}
export function PlanCard({ plan, onClick }: { plan: Plan; onClick: () => void }) {
  const date = new Date(plan.startsAt)
  return <button className="plan-card" onClick={onClick} aria-label={`View ${plan.title}`}>
    <span className="date-tile"><span>{date.toLocaleDateString(undefined, { month: 'short' })}</span><strong>{date.getDate()}</strong></span>
    <span className="plan-card-body"><span className="plan-card-top"><span className={`category-dot ${colors[plan.category] || 'gray'}`} />{plan.category}<span className="tiny-divider" />{dateLabel(plan.startsAt, { hour: 'numeric', minute: '2-digit' })}</span><strong>{plan.title}</strong><span className="plan-card-meta"><MapPin size={15} /> {plan.area} <span className="meta-separator">·</span> <UsersRound size={15} /> {statusText(plan)}</span></span>
    <span className="card-arrow"><ArrowUpRight size={20} /></span>
  </button>
}
