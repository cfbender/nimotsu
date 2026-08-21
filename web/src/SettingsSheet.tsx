import { FormEvent, useEffect, useState } from 'react'
import { Browser } from '@capacitor/browser'
import {
  beginGmailOAuth,
  disconnectGmail,
  getConnectionSettings,
  getGmailStatus,
  isNative,
  saveConnectionSettings,
  sendTestNotification,
  syncGmail,
  type GmailStatus,
} from './api'
import { enablePushNotifications } from './push'
import { getThemeMode, saveThemeMode, type ThemeMode } from './theme'
import { ConfirmDialog } from './ConfirmDialog'
import { Sheet } from './Sheet'

export function SettingsSheet({
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
  const [pushBusy, setPushBusy] = useState(false)
  const [gmailStatus, setGmailStatus] = useState<GmailStatus | null>(null)
  const [gmailAction, setGmailAction] = useState('')
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)
  const [disconnecting, setDisconnecting] = useState(false)

  useEffect(() => {
    void refreshGmailStatus()
  }, [])

  function save(event: FormEvent) {
    event.preventDefault()
    saveConnectionSettings({ serverURL, apiToken })
    onSaved()
  }

  async function enablePush() {
    setPushBusy(true)
    setPushState('Enabling…')
    try {
      saveConnectionSettings({ serverURL, apiToken })
      await enablePushNotifications()
      setPushState('Notifications enabled')
    } catch (error) {
      setPushState(error instanceof Error ? error.message : 'Could not enable notifications')
    } finally {
      setPushBusy(false)
    }
  }

  async function testPush() {
    setPushBusy(true)
    setPushState('Registering this device…')
    try {
      saveConnectionSettings({ serverURL, apiToken })
      await enablePushNotifications()
      setPushState('Sending test notification…')
      const result = await sendTestNotification()
      setPushState(result.sent === 1 ? 'Test notification sent' : `Test notification sent to ${result.sent} devices`)
    } catch (error) {
      setPushState(error instanceof Error ? error.message : 'Could not send test notification')
    } finally {
      setPushBusy(false)
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
    setGmailAction('Scanning Gmail…')
    try {
      const status = await syncGmail()
      setGmailStatus(status)
      setGmailAction(
        status.candidate_count > 0
          ? `${status.candidate_count} tracking suggestion${status.candidate_count === 1 ? '' : 's'} ready to review`
          : 'No tracking numbers found in recent mail',
      )
      onCandidatesChanged()
    } catch (error) {
      setGmailAction(error instanceof Error ? error.message : 'Could not scan Gmail')
    }
  }

  async function removeGmail() {
    setDisconnecting(true)
    setGmailAction('Disconnecting…')
    try {
      await disconnectGmail()
      await refreshGmailStatus()
      setGmailAction('Gmail disconnected')
      setConfirmDisconnect(false)
      onCandidatesChanged()
    } catch (error) {
      setGmailAction(error instanceof Error ? error.message : 'Could not disconnect Gmail')
    } finally {
      setDisconnecting(false)
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
          <p>Enable notifications, then send a test to verify the device, server, and Firebase connection.</p>
          <div className="push-actions">
            <button className="secondary-button full-width" disabled={pushBusy} type="button" onClick={() => void enablePush()}>Enable notifications</button>
            <button className="secondary-button full-width" disabled={pushBusy} type="button" onClick={() => void testPush()}>{pushBusy && pushState.startsWith('Sending') ? 'Sending…' : 'Send test notification'}</button>
          </div>
          {pushState && <p className="push-state" role="status">{pushState}</p>}
        </div>
      )}
      <div className="settings-section gmail-settings">
        <h3>Gmail discovery</h3>
        {!gmailStatus ? (
          <p>Checking Gmail configuration…</p>
        ) : !gmailStatus.configured ? (
          <p>Add the Google OAuth and encryption settings to the server to enable email discovery.</p>
        ) : gmailStatus.connected ? (
          <>
            <p><strong>{gmailStatus.email}</strong><br />Scans every five minutes. Messages stay in Gmail; only tracking suggestions are saved.</p>
            {gmailStatus.sync_error && <p className="form-error" role="alert">{gmailStatus.sync_error}</p>}
            <div className="gmail-actions">
              <button className="secondary-button" type="button" onClick={() => void scanGmail()}>Scan now</button>
              <button className="text-button danger-action" type="button" onClick={() => setConfirmDisconnect(true)}>Disconnect</button>
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
      {confirmDisconnect && (
        <ConfirmDialog
          title="Disconnect Gmail?"
          message="Nimotsu will remove the connection and pending email suggestions. Your messages remain untouched in Gmail."
          confirmLabel={disconnecting ? 'Disconnecting…' : 'Disconnect Gmail'}
          busy={disconnecting}
          onCancel={() => setConfirmDisconnect(false)}
          onConfirm={() => void removeGmail()}
        />
      )}
    </Sheet>
  )
}
