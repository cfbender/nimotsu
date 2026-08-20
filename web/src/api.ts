import { Capacitor } from '@capacitor/core'

const serverURLKey = 'nimotsu.serverURL'
const apiTokenKey = 'nimotsu.apiToken'

export interface TrackedPackage {
  id: string
  description: string
  tracking_number: string
  carrier_code: number | null
  status: string
  sub_status: string
  latest_message: string
  last_event_at: string | null
  tracking_error: string
  notifications_enabled: boolean
  created_at: string
  updated_at: string
}

export interface ConnectionSettings {
  serverURL: string
  apiToken: string
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

export async function addPackage(input: {
  description: string
  tracking_number: string
  carrier_code?: number
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
