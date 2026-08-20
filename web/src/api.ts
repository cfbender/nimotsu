import { Capacitor } from '@capacitor/core'

const serverURLKey = 'nimotsu.serverURL'
const apiTokenKey = 'nimotsu.apiToken'

export interface TrackedPackage {
  id: string
  description: string
  tracking_number: string
  carrier: string
  status: string
  sub_status: string
  latest_message: string
  last_event_at: string | null
  tracking_error: string
  notifications_enabled: boolean
  created_at: string
  updated_at: string
}

export interface TrackingEvent {
  status: string
  sub_status: string
  message: string
  occurred_at: string
}

export interface ConnectionSettings {
  serverURL: string
  apiToken: string
}

export interface GmailStatus {
  configured: boolean
  connected: boolean
  email?: string
  last_sync_at?: string
  sync_error?: string
  candidate_count: number
}

export interface EmailCandidate {
  id: string
  tracking_number: string
  description: string
  sender: string
  message_at: string
  created_at: string
}

export function isNative(): boolean {
  return Capacitor.isNativePlatform()
}

export function getConnectionSettings(): ConnectionSettings {
  return {
    serverURL: localStorage.getItem(serverURLKey) ?? '',
    apiToken: localStorage.getItem(apiTokenKey) ?? '',
  }
}

export function saveConnectionSettings(settings: ConnectionSettings): void {
  const serverURL = settings.serverURL.trim().replace(/\/+$/, '')
  localStorage.setItem(serverURLKey, serverURL)
  localStorage.setItem(apiTokenKey, settings.apiToken.trim())
}

export async function listPackages(): Promise<TrackedPackage[]> {
  return request<TrackedPackage[]>('/api/packages')
}

export async function listTrackingEvents(id: string): Promise<TrackingEvent[]> {
  return request<TrackingEvent[]>(`/api/packages/${id}/events`)
}

export async function detectCarrier(trackingNumber: string): Promise<string> {
  const params = new URLSearchParams({ tracking_number: trackingNumber })
  const response = await request<{ carrier: string }>(`/api/tracking/carrier?${params}`)
  return response.carrier
}

export async function addPackage(input: {
  description: string
  tracking_number: string
  carrier?: string
}): Promise<TrackedPackage> {
  return request<TrackedPackage>('/api/packages', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function setPackageNotifications(id: string, enabled: boolean): Promise<TrackedPackage> {
  return request<TrackedPackage>(`/api/packages/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ notifications_enabled: enabled }),
  })
}

export async function archivePackage(id: string): Promise<void> {
  await request<void>(`/api/packages/${id}`, { method: 'DELETE' })
}

export async function registerDevice(token: string): Promise<void> {
  await request<void>('/api/devices', {
    method: 'POST',
    body: JSON.stringify({ token, platform: 'android' }),
  })
}

export async function getGmailStatus(): Promise<GmailStatus> {
  return request<GmailStatus>('/api/gmail/status')
}

export async function beginGmailOAuth(): Promise<string> {
  const response = await request<{ url: string }>('/api/gmail/oauth/start', { method: 'POST' })
  return response.url
}

export async function syncGmail(): Promise<GmailStatus> {
  return request<GmailStatus>('/api/gmail/sync', { method: 'POST' })
}

export async function disconnectGmail(): Promise<void> {
  await request<void>('/api/gmail', { method: 'DELETE' })
}

export async function listEmailCandidates(): Promise<EmailCandidate[]> {
  return request<EmailCandidate[]>('/api/gmail/candidates')
}

export async function acceptEmailCandidate(id: string): Promise<TrackedPackage> {
  return request<TrackedPackage>(`/api/gmail/candidates/${id}/accept`, { method: 'POST' })
}

export async function dismissEmailCandidate(id: string): Promise<void> {
  await request<void>(`/api/gmail/candidates/${id}`, { method: 'DELETE' })
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const settings = getConnectionSettings()
  const headers = new Headers(init.headers)
  if (init.body) headers.set('Content-Type', 'application/json')
  if (settings.apiToken) headers.set('Authorization', `Bearer ${settings.apiToken}`)

  const response = await fetch(`${settings.serverURL}${path}`, { ...init, headers })
  if (!response.ok) {
    let message = `Request failed (${response.status})`
    try {
      const body = (await response.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // The fallback includes enough detail when a proxy or network edge returns HTML.
    }
    throw new Error(message)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}
