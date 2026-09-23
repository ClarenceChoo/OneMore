export type User = { id: string; name: string; email: string }
export type Plan = {
  id: string; hostId: string; hostName: string; title: string; description: string
  category: string; tags: string[]; area: string; location: string
  startsAt: string; joinDeadline: string; spaces: number; confirmed: number
  reserved: number; waiting: number; available: number; status: 'active' | 'cancelled'
  myStatus: 'host' | 'confirmed' | 'waitlisted' | 'offered' | 'left' | ''
  offerExpiresAt?: string
  recommendationReason?: string
}
export type Notification = { id: string; message: string; invitationId: string; read: boolean; createdAt: string }
export type FeedPage = { plans: Plan[]; nextCursor?: string }
export type AuthConfig = { localAuth: boolean; oidc: boolean }
export type DiscoveryPreferences = { categories: string[]; area: string; availability: 'any' | 'weekday_evenings' | 'weekends'; timezone: string }

export class ApiError extends Error {
  constructor(public code: string, message: string, public status: number) { super(message) }
}
export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...options, headers: { ...(options?.body ? { 'Content-Type': 'application/json' } : {}), ...options?.headers } })
  if (!response.ok) {
    let body: { code?: string; message?: string } = {}
    try { body = await response.json() } catch { /* keep fallback */ }
    throw new ApiError(body.code || 'request_failed', body.message || 'The request failed. Try again.', response.status)
  }
  if (response.status === 204) return undefined as T
  return response.json()
}
export const post = <T>(path: string, body?: unknown, headers?: HeadersInit) => request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body), headers })

export function dateLabel(value: string, options?: Intl.DateTimeFormatOptions) {
  return new Intl.DateTimeFormat(undefined, options || { weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(value))
}
export function timeUntil(value: string) {
  const minutes = Math.max(0, Math.ceil((new Date(value).getTime() - Date.now()) / 60000))
  return minutes < 60 ? `${minutes} min left` : `${Math.floor(minutes / 60)} hr ${minutes % 60} min left`
}
