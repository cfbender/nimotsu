import { FormEvent, useCallback, useEffect, useState } from 'react'
import {
  addPackage,
  archivePackage,
  getConnectionSettings,
  isNative,
  listPackages,
  saveConnectionSettings,
  setPackageNotifications,
  type TrackedPackage,
} from './api'
import { formatRelativeDate, formatStatus } from './format'
import { enablePushNotifications } from './push'
import { getThemeMode, saveThemeMode, type ThemeMode } from './theme'

type Sheet = 'add' | 'settings' | null

export default function App() {
  const [packages, setPackages] = useState<TrackedPackage[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [sheet, setSheet] = useState<Sheet>(isNative() && !getConnectionSettings().serverURL ? 'settings' : null)
  const [connectionVersion, setConnectionVersion] = useState(0)

  const loadPackages = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setPackages(await listPackages())
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not load packages')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!isNative() || getConnectionSettings().serverURL) void loadPackages()
  }, [connectionVersion, loadPackages])

  async function toggleNotifications(pkg: TrackedPackage) {
    try {
      const updated = await setPackageNotifications(pkg.id, !pkg.notifications_enabled)
      setPackages((current) => current.map((item) => (item.id === updated.id ? updated : item)))
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not update notifications')
    }
  }

  async function removePackage(pkg: TrackedPackage) {
    if (!window.confirm(`Archive “${pkg.description}”?`)) return
    try {
      await archivePackage(pkg.id)
      setPackages((current) => current.filter((item) => item.id !== pkg.id))
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not archive package')
    }
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <div>
          <p className="eyebrow">Your deliveries</p>
          <h1>Nimotsu</h1>
        </div>
        <button className="icon-button" type="button" aria-label="Open settings" onClick={() => setSheet('settings')}>
          <SettingsIcon />
        </button>
      </header>

      <main>
        {error && (
          <div className="error-banner" role="alert">
            <span>{error}</span>
            <button type="button" onClick={() => void loadPackages()}>Retry</button>
          </div>
        )}

        {loading ? (
          <div className="loading" aria-label="Loading packages"><span /><span /><span /></div>
        ) : packages.length === 0 ? (
          <section className="empty-state">
            <div className="empty-illustration"><PackageIcon /></div>
            <h2>Nothing in transit</h2>
            <p>Add a tracking number and we’ll keep an eye on it.</p>
            <button className="primary-button" type="button" onClick={() => setSheet('add')}>Add your first package</button>
          </section>
        ) : (
          <section className="package-list" aria-label="Packages">
            {packages.map((pkg) => (
              <article className={`package-card status-${pkg.status.toLowerCase()}`} key={pkg.id}>
                <div className="package-card-main">
                  <div className="status-mark"><PackageIcon /></div>
                  <div className="package-copy">
                    <div className="package-heading">
                      <h2>{pkg.description}</h2>
                      <span className="status-pill">{formatStatus(pkg.status)}</span>
                    </div>
                    <p className="latest-message">{pkg.latest_message || pkg.tracking_error || 'Waiting for a tracking update'}</p>
                    <div className="package-meta">
                      <span>{pkg.tracking_number}</span>
                      <span className="package-update"><span aria-hidden="true">·</span> {formatRelativeDate(pkg.last_event_at)}</span>
                    </div>
                  </div>
                </div>
                <div className="card-actions">
                  <button type="button" onClick={() => void toggleNotifications(pkg)} aria-pressed={pkg.notifications_enabled}>
                    <BellIcon enabled={pkg.notifications_enabled} />
                    {pkg.notifications_enabled ? 'Updates on' : 'Updates off'}
                  </button>
                  <button className="danger-action" type="button" onClick={() => void removePackage(pkg)}>Archive</button>
                </div>
              </article>
            ))}
          </section>
        )}
      </main>

      {packages.length > 0 && (
        <button className="floating-button" type="button" onClick={() => setSheet('add')} aria-label="Add package">
          <PlusIcon />
        </button>
      )}

      {sheet === 'add' && (
        <AddPackageSheet
          onClose={() => setSheet(null)}
          onAdded={(pkg) => {
            setPackages((current) => [pkg, ...current])
            setSheet(null)
          }}
        />
      )}
      {sheet === 'settings' && (
        <SettingsSheet
          onClose={() => setSheet(null)}
          onSaved={() => {
            setSheet(null)
            setConnectionVersion((value) => value + 1)
          }}
        />
      )}
    </div>
  )
}

function AddPackageSheet({ onClose, onAdded }: { onClose: () => void; onAdded: (pkg: TrackedPackage) => void }) {
  const [description, setDescription] = useState('')
  const [trackingNumber, setTrackingNumber] = useState('')
  const [carrierCode, setCarrierCode] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      const parsedCarrier = carrierCode ? Number(carrierCode) : undefined
      const pkg = await addPackage({
        description: description.trim(),
        tracking_number: trackingNumber.trim(),
        ...(parsedCarrier ? { carrier_code: parsedCarrier } : {}),
      })
      onAdded(pkg)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not add package')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Sheet title="Add a package" onClose={onClose}>
      <form onSubmit={(event) => void submit(event)}>
        <label>
          What is it?
          <input autoFocus required value={description} onChange={(event) => setDescription(event.target.value)} placeholder="New headphones" />
        </label>
        <label>
          Tracking number
          <input required autoCapitalize="characters" value={trackingNumber} onChange={(event) => setTrackingNumber(event.target.value)} placeholder="1Z999AA10123456784" />
        </label>
        <label>
          Carrier code <span className="optional">optional</span>
          <input inputMode="numeric" pattern="[0-9]*" value={carrierCode} onChange={(event) => setCarrierCode(event.target.value)} placeholder="We’ll ask 17TRACK to detect it" />
        </label>
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="primary-button full-width" disabled={saving} type="submit">{saving ? 'Adding…' : 'Track package'}</button>
      </form>
    </Sheet>
  )
}

function SettingsSheet({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const current = getConnectionSettings()
  const [serverURL, setServerURL] = useState(current.serverURL)
  const [apiToken, setAPIToken] = useState(current.apiToken)
  const [themeMode, setThemeMode] = useState(getThemeMode)
  const [pushState, setPushState] = useState('')

  function save(event: FormEvent) {
    event.preventDefault()
    saveConnectionSettings({ serverURL, apiToken })
    onSaved()
  }

  async function enablePush() {
    setPushState('Enabling…')
    try {
      saveConnectionSettings({ serverURL, apiToken })
      await enablePushNotifications()
      setPushState('Notifications enabled')
    } catch (error) {
      setPushState(error instanceof Error ? error.message : 'Could not enable notifications')
    }
  }

  function selectTheme(mode: ThemeMode) {
    setThemeMode(mode)
    saveThemeMode(mode)
  }

  return (
    <Sheet title="Settings" onClose={onClose}>
      <fieldset className="theme-fieldset">
        <legend>Appearance</legend>
        <div className="theme-options">
          {(['system', 'light', 'dark'] as const).map((mode) => (
            <button key={mode} type="button" aria-pressed={themeMode === mode} onClick={() => selectTheme(mode)}>
              {mode[0].toUpperCase() + mode.slice(1)}
            </button>
          ))}
        </div>
        <p>System follows your device appearance.</p>
      </fieldset>
      <form onSubmit={save}>
        {isNative() && (
          <label>
            Server URL
            <input type="url" required value={serverURL} onChange={(event) => setServerURL(event.target.value)} placeholder="https://packages.example.com" />
          </label>
        )}
        <label>
          API token <span className="optional">if configured</span>
          <input type="password" value={apiToken} onChange={(event) => setAPIToken(event.target.value)} autoComplete="off" />
        </label>
        <button className="primary-button full-width" type="submit">Save connection</button>
      </form>
      {isNative() && (
        <div className="settings-section">
          <h3>Push notifications</h3>
          <p>Android will ask for notification permission when you enable this.</p>
          <button className="secondary-button full-width" type="button" onClick={() => void enablePush()}>Enable notifications</button>
          {pushState && <p className="push-state" role="status">{pushState}</p>}
        </div>
      )}
      <div className="settings-section coming-soon">
        <div><h3>Gmail</h3><p>Automatic tracking-number suggestions are the next integration.</p></div>
        <span>Coming next</span>
      </div>
    </Sheet>
  )
}

function Sheet({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="sheet-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <section className="sheet" role="dialog" aria-modal="true" aria-labelledby="sheet-title">
        <div className="sheet-handle" />
        <div className="sheet-heading"><h2 id="sheet-title">{title}</h2><button type="button" onClick={onClose} aria-label="Close"><CloseIcon /></button></div>
        {children}
      </section>
    </div>
  )
}

function PackageIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m4 7.5 8-4 8 4v9l-8 4-8-4zM4 7.5l8 4 8-4M12 11.5v9M8 5.5l8 4" /></svg>
}

function BellIcon({ enabled }: { enabled: boolean }) {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4" />{!enabled && <path d="m4 4 16 16" />}</svg>
}

function SettingsIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-1.6v-.2h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z" /></svg>
}

function PlusIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
}

function CloseIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
}
