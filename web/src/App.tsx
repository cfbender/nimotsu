import { FormEvent, useCallback, useEffect, useState } from 'react'
import { Browser } from '@capacitor/browser'
import {
  acceptEmailCandidate,
  addPackage,
  archivePackage,
  beginGmailOAuth,
  disconnectGmail,
  dismissEmailCandidate,
  getConnectionSettings,
  getGmailStatus,
  isNative,
  listEmailCandidates,
  listPackages,
  saveConnectionSettings,
  setPackageNotifications,
  syncGmail,
  type EmailCandidate,
  type GmailStatus,
  type TrackedPackage,
} from './api'
import { formatCarrier, formatRelativeDate, formatStatus } from './format'
import { enablePushNotifications } from './push'
import { getThemeMode, saveThemeMode, type ThemeMode } from './theme'

type Sheet = 'add' | 'settings' | null

export default function App() {
  const [packages, setPackages] = useState<TrackedPackage[]>([])
  const [candidates, setCandidates] = useState<EmailCandidate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [sheet, setSheet] = useState<Sheet>(
    (isNative() && !getConnectionSettings().serverURL) || new URLSearchParams(window.location.search).has('gmail') ? 'settings' : null,
  )
  const [connectionVersion, setConnectionVersion] = useState(0)

  const loadData = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextPackages, nextCandidates] = await Promise.all([listPackages(), listEmailCandidates()])
      setPackages(nextPackages)
      setCandidates(nextCandidates)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not load deliveries')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (new URLSearchParams(window.location.search).has('gmail')) {
      window.history.replaceState({}, '', window.location.pathname)
    }
    if (!isNative() || getConnectionSettings().serverURL) void loadData()
  }, [connectionVersion, loadData])

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

  async function trackCandidate(candidate: EmailCandidate) {
    try {
      const pkg = await acceptEmailCandidate(candidate.id)
      setCandidates((current) => current.filter((item) => item.id !== candidate.id))
      setPackages((current) => [pkg, ...current])
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not track email suggestion')
    }
  }

  async function dismissCandidate(candidate: EmailCandidate) {
    try {
      await dismissEmailCandidate(candidate.id)
      setCandidates((current) => current.filter((item) => item.id !== candidate.id))
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not dismiss email suggestion')
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
            <button type="button" onClick={() => void loadData()}>Retry</button>
          </div>
        )}

        {loading ? (
          <div className="loading" aria-label="Loading packages"><span /><span /><span /></div>
        ) : (
          <>
            {candidates.length > 0 && (
              <CandidateList candidates={candidates} onTrack={trackCandidate} onDismiss={dismissCandidate} />
            )}
            {packages.length === 0 ? (
              <section className={`empty-state${candidates.length > 0 ? ' compact' : ''}`}>
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
                          {pkg.carrier && <><span aria-hidden="true">·</span><span>{formatCarrier(pkg.carrier)}</span></>}
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
          </>
        )}
      </main>

      {packages.length > 0 && !sheet && (
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
          onCandidatesChanged={() => void loadData()}
          onSaved={() => {
            setSheet(null)
            setConnectionVersion((value) => value + 1)
          }}
        />
      )}
    </div>
  )
}

function CandidateList({
  candidates,
  onTrack,
  onDismiss,
}: {
  candidates: EmailCandidate[]
  onTrack: (candidate: EmailCandidate) => Promise<void>
  onDismiss: (candidate: EmailCandidate) => Promise<void>
}) {
  const [workingID, setWorkingID] = useState('')

  async function run(candidate: EmailCandidate, action: (candidate: EmailCandidate) => Promise<void>) {
    setWorkingID(candidate.id)
    try {
      await action(candidate)
    } finally {
      setWorkingID('')
    }
  }

  return (
    <section className="candidate-section" aria-labelledby="candidate-heading">
      <div className="section-heading">
        <div>
          <p className="eyebrow">From Gmail</p>
          <h2 id="candidate-heading">Ready to review</h2>
        </div>
        <span>{candidates.length}</span>
      </div>
      <div className="candidate-list">
        {candidates.map((candidate) => (
          <article className="candidate-card" key={candidate.id}>
            <div className="candidate-icon"><MailIcon /></div>
            <div className="candidate-copy">
              <h3>{candidate.description}</h3>
              <p>
                <span>{candidate.sender || 'Gmail'}</span>
                <span aria-hidden="true">·</span>
                <time dateTime={candidate.message_at}>{new Date(candidate.message_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</time>
              </p>
              <code>{candidate.tracking_number}</code>
            </div>
            <div className="candidate-actions">
              <button className="primary-button" disabled={workingID === candidate.id} type="button" onClick={() => void run(candidate, onTrack)}>Track</button>
              <button disabled={workingID === candidate.id} type="button" onClick={() => void run(candidate, onDismiss)}>Dismiss</button>
            </div>
          </article>
        ))}
      </div>
    </section>
  )
}

function AddPackageSheet({ onClose, onAdded }: { onClose: () => void; onAdded: (pkg: TrackedPackage) => void }) {
  const [description, setDescription] = useState('')
  const [trackingNumber, setTrackingNumber] = useState('')
  const [carrier, setCarrier] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      const pkg = await addPackage({
        description: description.trim(),
        tracking_number: trackingNumber.trim(),
        ...(carrier.trim() ? { carrier: carrier.trim() } : {}),
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
          Carrier <span className="optional">optional</span>
          <input list="carrier-options" value={carrier} onChange={(event) => setCarrier(event.target.value)} placeholder="Auto-detect" />
          <datalist id="carrier-options">
            <option value="usps">USPS</option>
            <option value="ups">UPS</option>
            <option value="fedex">FedEx</option>
            <option value="dhl_express">DHL Express</option>
          </datalist>
          <span className="field-help">Leave blank to detect common carriers, or enter a Shippo carrier token.</span>
        </label>
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="primary-button full-width" disabled={saving} type="submit">{saving ? 'Adding…' : 'Track package'}</button>
      </form>
    </Sheet>
  )
}

function SettingsSheet({
  onClose,
  onSaved,
  onCandidatesChanged,
}: {
  onClose: () => void
  onSaved: () => void
  onCandidatesChanged: () => void
}) {
  const current = getConnectionSettings()
  const [serverURL, setServerURL] = useState(current.serverURL)
  const [apiToken, setAPIToken] = useState(current.apiToken)
  const [themeMode, setThemeMode] = useState(getThemeMode)
  const [pushState, setPushState] = useState('')
  const [gmailStatus, setGmailStatus] = useState<GmailStatus | null>(null)
  const [gmailAction, setGmailAction] = useState('')

  useEffect(() => {
    void refreshGmailStatus()
  }, [])

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

  async function refreshGmailStatus() {
    try {
      setGmailStatus(await getGmailStatus())
    } catch (error) {
      setGmailAction(error instanceof Error ? error.message : 'Could not read Gmail status')
    }
  }

  async function linkGmail() {
    setGmailAction('Opening Google…')
    try {
      saveConnectionSettings({ serverURL, apiToken })
      const authorizationURL = await beginGmailOAuth()
      if (isNative()) {
        const listener = await Browser.addListener('browserFinished', () => {
          void refreshGmailStatus()
          void listener.remove()
        })
        await Browser.open({ url: authorizationURL })
        setGmailAction('Finish linking in the browser, then return here.')
      } else {
        window.location.assign(authorizationURL)
      }
    } catch (error) {
      setGmailAction(error instanceof Error ? error.message : 'Could not link Gmail')
    }
  }

  async function scanGmail() {
    setGmailAction('Scanning inbox…')
    try {
      setGmailStatus(await syncGmail())
      setGmailAction('Inbox scan complete')
      onCandidatesChanged()
    } catch (error) {
      setGmailAction(error instanceof Error ? error.message : 'Could not scan Gmail')
    }
  }

  async function removeGmail() {
    if (!window.confirm('Disconnect Gmail and remove its pending suggestions?')) return
    setGmailAction('Disconnecting…')
    try {
      await disconnectGmail()
      await refreshGmailStatus()
      setGmailAction('Gmail disconnected')
      onCandidatesChanged()
    } catch (error) {
      setGmailAction(error instanceof Error ? error.message : 'Could not disconnect Gmail')
    }
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
          Nimotsu API token <span className="optional">if configured</span>
          <input type="password" value={apiToken} onChange={(event) => setAPIToken(event.target.value)} autoComplete="off" />
          <span className="field-help">Matches NIMOTSU_API_TOKEN. Shippo credentials stay on the server.</span>
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
      <div className="settings-section gmail-settings">
        <h3>Gmail discovery</h3>
        {!gmailStatus ? (
          <p>Checking Gmail configuration…</p>
        ) : !gmailStatus.configured ? (
          <p>Add the Google OAuth and encryption settings to the server to enable inbox discovery.</p>
        ) : gmailStatus.connected ? (
          <>
            <p><strong>{gmailStatus.email}</strong><br />Scans every five minutes. Messages stay in Gmail; only tracking suggestions are saved.</p>
            {gmailStatus.sync_error && <p className="form-error" role="alert">{gmailStatus.sync_error}</p>}
            <div className="gmail-actions">
              <button className="secondary-button" type="button" onClick={() => void scanGmail()}>Scan now</button>
              <button className="text-button danger-action" type="button" onClick={() => void removeGmail()}>Disconnect</button>
            </div>
          </>
        ) : (
          <>
            <p>Find tracking numbers in shipping emails and review them before adding packages.</p>
            <button className="secondary-button full-width" type="button" onClick={() => void linkGmail()}>Link Gmail</button>
          </>
        )}
        {gmailAction && <p className="push-state" role="status">{gmailAction}</p>}
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

function MailIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2" /><path d="m4 7 8 6 8-6" /></svg>
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
