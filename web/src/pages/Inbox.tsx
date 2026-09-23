import { useEffect, useState } from 'react'
import { Bell, Check } from 'lucide-react'
import { type Notification, dateLabel, post, request } from '../api'

export function Inbox({ navigate, revision }: { navigate: (to: string) => void; revision: number }) {
  const [items, setItems] = useState<Notification[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  useEffect(() => { request<Notification[]>('/api/notifications').then(setItems).catch((e: Error) => setError(e.message)).finally(() => setLoading(false)) }, [revision])
  async function open(item: Notification) { try { if (!item.read) { await post(`/api/notifications/${item.id}/read`); setItems(old => old.map(n => n.id === item.id ? { ...n, read: true } : n)) } navigate(`/plans/${item.invitationId}`) } catch (e) { setError((e as Error).message) } }
  return <div className="content-wrap standard-page"><div className="page-head"><div><h1>Inbox</h1><p>Updates about plans you’ve joined.</p></div></div>{error && <p className="inline-error" role="alert">{error}</p>}{loading ? <div className="list-loading">Loading updates…</div> : items.length ? <div className="notification-list">{items.map(item => <button key={item.id} className={`notification ${item.read ? 'read' : ''}`} onClick={() => open(item)}><span className="notification-dot">{item.read ? <Check size={16} /> : <Bell size={16} />}</span><span><strong>{item.message}</strong><small>{dateLabel(item.createdAt)}</small></span></button>)}</div> : <div className="empty-state"><div className="empty-icon"><Bell size={24} /></div><h3>All caught up</h3><p>When there’s news about your plans, you’ll see it here.</p><button className="secondary-button" onClick={() => navigate('/')}>Explore plans</button></div>}</div>
}
