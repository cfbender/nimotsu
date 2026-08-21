import { FormEvent, useEffect, useState } from 'react'
import { Browser } from '@capacitor/browser'
import { Clipboard } from '@capacitor/clipboard'
import { isNative, listTrackingEvents, type TrackingEvent, type TrackedPackage } from './api'
import { formatCarrier, formatEstimatedDelivery, formatEventDate, formatStatus, getCarrierTrackingURL } from './format'
import { Sheet } from './Sheet'
import { BellIcon, CheckIcon, ClockIcon, CopyIcon, EditIcon, ExternalLinkIcon, LocationIcon, PackageIcon } from './icons'

export function PackageDetailSheet({
  pkg,
  onClose,
  onRename,
  onToggleNotifications,
  onArchive,
}: {
  pkg: TrackedPackage
  onClose: () => void
  onRename: (description: string) => Promise<void>
  onToggleNotifications: () => void
  onArchive: () => void
}) {
  const [events, setEvents] = useState<TrackingEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [renaming, setRenaming] = useState(false)
  const [description, setDescription] = useState(pkg.description)
  const [renameError, setRenameError] = useState('')
  const [savingName, setSavingName] = useState(false)
  const [trackingCopied, setTrackingCopied] = useState(false)
  const trackingURL = getCarrierTrackingURL(pkg.carrier, pkg.tracking_number)

  useEffect(() => setDescription(pkg.description), [pkg.description])

  useEffect(() => {
    if (!trackingCopied) return
    const timeout = window.setTimeout(() => setTrackingCopied(false), 2_000)
    return () => window.clearTimeout(timeout)
  }, [trackingCopied])

  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    void listTrackingEvents(pkg.id)
      .then((nextEvents) => active && setEvents(nextEvents))
      .catch((requestError) => active && setError(requestError instanceof Error ? requestError.message : 'Could not load tracking history'))
      .finally(() => active && setLoading(false))
    return () => { active = false }
  }, [pkg.id])

  async function submitRename(event: FormEvent) {
    event.preventDefault()
    const nextDescription = description.trim()
    if (!nextDescription) {
      setRenameError('Package name is required')
      return
    }
    if (nextDescription === pkg.description) {
      setRenaming(false)
      return
    }
    setSavingName(true)
    setRenameError('')
    try {
      await onRename(nextDescription)
      setRenaming(false)
    } catch (requestError) {
      setRenameError(requestError instanceof Error ? requestError.message : 'Could not rename package')
    } finally {
      setSavingName(false)
    }
  }

  function openTrackingPage(event: React.MouseEvent<HTMLAnchorElement>) {
    if (!trackingURL || !isNative()) return
    event.preventDefault()
    void Browser.open({ url: trackingURL })
  }

  async function copyTrackingNumber() {
    try {
      await Clipboard.write({ string: pkg.tracking_number })
      setTrackingCopied(true)
    } catch {
      setTrackingCopied(false)
    }
  }

  return (
    <Sheet title="Shipment details" onClose={onClose}>
      <div className={`detail-hero status-${pkg.status.toLowerCase()}`}>
        <div className="detail-status-mark"><PackageIcon /></div>
        <div className="detail-copy">
          <span className="status-pill">{formatStatus(pkg.status)}</span>
          {renaming ? (
            <form className="rename-form" onSubmit={(event) => void submitRename(event)}>
              <input autoFocus required aria-label="Package name" value={description} onChange={(event) => setDescription(event.target.value)} />
              <div>
                <button type="button" disabled={savingName} onClick={() => {
                  setDescription(pkg.description)
                  setRenameError('')
                  setRenaming(false)
                }}>Cancel</button>
                <button className="rename-save" type="submit" disabled={savingName}>{savingName ? 'Saving…' : 'Save'}</button>
              </div>
              {renameError && <p className="rename-error" role="alert">{renameError}</p>}
            </form>
          ) : (
            <div className="detail-title">
              <h3>{pkg.description}</h3>
              <button className="rename-trigger" type="button" aria-label="Rename package" onClick={() => setRenaming(true)}><EditIcon /></button>
            </div>
          )}
          <p>
            {pkg.latest_message || pkg.tracking_error || 'Waiting for a tracking update'}
            {pkg.latest_location && <span className="latest-location"><LocationIcon />{pkg.latest_location}</span>}
          </p>
        </div>
      </div>

      <dl className="shipment-facts">
        <div><dt>Carrier</dt><dd>{pkg.carrier ? formatCarrier(pkg.carrier) : 'Not detected'}</dd></div>
        <div><dt>Estimated delivery</dt><dd>{pkg.estimated_delivery_at ? formatEstimatedDelivery(pkg.estimated_delivery_at) : 'Not available yet'}</dd></div>
        <div className="tracking-number-fact">
          <dt>Tracking number</dt>
          <dd>
            <span className="tracking-number-value">
              {trackingURL ? (
                <a href={trackingURL} target="_blank" rel="noreferrer" onClick={openTrackingPage}>
                  {pkg.tracking_number}<ExternalLinkIcon />
                </a>
              ) : pkg.tracking_number}
            </span>
            <button
              className="copy-tracking-button"
              type="button"
              aria-label={trackingCopied ? 'Tracking number copied' : 'Copy tracking number'}
              title={trackingCopied ? 'Copied' : 'Copy tracking number'}
              onClick={() => void copyTrackingNumber()}
            >
              {trackingCopied ? <CheckIcon /> : <CopyIcon />}
            </button>
          </dd>
        </div>
      </dl>

      <div className="detail-actions">
        <button className="secondary-button" type="button" aria-pressed={pkg.notifications_enabled} onClick={onToggleNotifications}>
          <BellIcon enabled={pkg.notifications_enabled} />
          {pkg.notifications_enabled ? 'Updates on' : 'Updates off'}
        </button>
        <button className="text-button danger-action" type="button" onClick={onArchive}>Archive</button>
      </div>

      <section className="history-section" aria-labelledby="history-heading">
        <div className="history-heading">
          <div><p className="eyebrow">Tracking activity</p><h3 id="history-heading">Update history</h3></div>
          {!loading && <span>{events.length}</span>}
        </div>
        {loading ? (
          <div className="history-loading" aria-label="Loading tracking history"><span /><span /><span /></div>
        ) : error ? (
          <p className="form-error" role="alert">{error}</p>
        ) : events.length === 0 ? (
          <div className="history-empty"><ClockIcon /><p>No carrier scans yet. New updates will appear here.</p></div>
        ) : (
          <ol className="tracking-timeline">
            {events.map((event, index) => (
              <li key={`${event.occurred_at}-${event.status}-${event.sub_status}-${index}`}>
                <span className="timeline-dot" aria-hidden="true" />
                <div className="timeline-copy">
                  <div><strong>{formatStatus(event.sub_status || event.status)}</strong><time dateTime={event.occurred_at}>{formatEventDate(event.occurred_at)}</time></div>
                  <p>{event.message || (event.sub_status ? formatStatus(event.sub_status) : formatStatus(event.status))}</p>
                  {event.location && <span className="timeline-location"><LocationIcon />{event.location}</span>}
                </div>
              </li>
            ))}
          </ol>
        )}
      </section>
    </Sheet>
  )
}
