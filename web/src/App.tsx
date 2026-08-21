import { useCallback, useEffect, useRef, useState } from 'react'
import {
  acceptEmailCandidate,
  archivePackage,
  dismissEmailCandidate,
  getConnectionSettings,
  isNative,
  listEmailCandidates,
  listPackages,
  refreshTracking,
  renamePackage,
  setPackageNotificationSettings,
  type EmailCandidate,
  type NotificationSettings,
  type TrackedPackage,
} from './api'
import { formatCarrier, formatEstimatedDelivery, formatRelativeDate, formatStatus } from './format'
import { AddPackageSheet } from './AddPackageSheet'
import { CandidateList } from './CandidateList'
import { ConfirmDialog } from './ConfirmDialog'
import { PackageDetailSheet } from './PackageDetailSheet'
import { SettingsSheet } from './SettingsSheet'
import { BellIcon, ChevronIcon, LocationIcon, PackageIcon, PlusIcon, RefreshIcon, SettingsIcon } from './icons'

type Sheet = 'add' | 'settings' | null

function notificationLabel(pkg: TrackedPackage): string {
  if (!pkg.notifications_enabled) return 'Updates off'
  const choices = [pkg.notify_in_transit, pkg.notify_out_for_delivery, pkg.notify_delivered, pkg.notify_exceptions]
  if (!choices.some(Boolean)) return 'No alerts selected'
  return choices.every(Boolean) ? 'Updates on' : 'Custom alerts'
}

export default function App() {
  const [packages, setPackages] = useState<TrackedPackage[]>([])
  const [candidates, setCandidates] = useState<EmailCandidate[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [pullDistance, setPullDistance] = useState(0)
  const [error, setError] = useState('')
  const [sheet, setSheet] = useState<Sheet>(
    (isNative() && !getConnectionSettings().serverURL) || new URLSearchParams(window.location.search).has('gmail') ? 'settings' : null,
  )
  const [selectedPackage, setSelectedPackage] = useState<TrackedPackage | null>(null)
  const [archiveTarget, setArchiveTarget] = useState<TrackedPackage | null>(null)
  const [archiving, setArchiving] = useState(false)
  const [connectionVersion, setConnectionVersion] = useState(0)
  const pullRef = useRef<{ startX: number; startY: number; distance: number } | null>(null)

  const loadData = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextPackages, nextCandidates] = await Promise.all([listPackages(), listEmailCandidates()])
      setPackages(nextPackages)
      setSelectedPackage((current) => current ? nextPackages.find((pkg) => pkg.id === current.id) ?? null : null)
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

  const updateTracking = useCallback(async () => {
    if (refreshing) return
    setRefreshing(true)
    setError('')
    try {
      const result = await refreshTracking()
      setPackages(result.packages)
      setSelectedPackage((current) => current ? result.packages.find((pkg) => pkg.id === current.id) ?? null : null)
      if (result.failed > 0) {
        setError(`${result.failed} ${result.failed === 1 ? 'package' : 'packages'} could not be refreshed`)
      }
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not refresh tracking')
    } finally {
      setRefreshing(false)
    }
  }, [refreshing])

  function startPull(event: React.TouchEvent<HTMLDivElement>) {
    if (!window.matchMedia('(max-width: 639px)').matches || window.scrollY > 0 || event.touches.length !== 1 || loading || refreshing || packages.length === 0 || sheet || selectedPackage || archiveTarget) return
    pullRef.current = { startX: event.touches[0].clientX, startY: event.touches[0].clientY, distance: 0 }
  }

  function movePull(event: React.TouchEvent<HTMLDivElement>) {
    const pull = pullRef.current
    if (!pull || event.touches.length !== 1) return
    const horizontal = Math.abs(event.touches[0].clientX - pull.startX)
    const vertical = event.touches[0].clientY - pull.startY
    if (horizontal > Math.max(12, vertical)) {
      pullRef.current = null
      setPullDistance(0)
      return
    }
    if (vertical <= 0 || window.scrollY > 0) {
      pull.distance = 0
      setPullDistance(0)
      return
    }
    event.preventDefault()
    pull.distance = Math.min(76, vertical * 0.5)
    setPullDistance(pull.distance)
  }

  function finishPull(cancelled = false) {
    const pull = pullRef.current
    if (!pull) return
    pullRef.current = null
    if (!cancelled && pull.distance >= 56) void updateTracking()
    setPullDistance(0)
  }

  async function updatePackageNotifications(pkg: TrackedPackage, settings: NotificationSettings) {
    const updated = await setPackageNotificationSettings(pkg.id, settings)
    setPackages((current) => current.map((item) => (item.id === updated.id ? updated : item)))
    setSelectedPackage((current) => current?.id === updated.id ? updated : current)
  }

  async function toggleNotifications(pkg: TrackedPackage) {
    try {
      await updatePackageNotifications(pkg, {
        notifications_enabled: !pkg.notifications_enabled,
        notify_in_transit: pkg.notify_in_transit,
        notify_out_for_delivery: pkg.notify_out_for_delivery,
        notify_delivered: pkg.notify_delivered,
        notify_exceptions: pkg.notify_exceptions,
      })
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not update notifications')
    }
  }

  async function renameTrackedPackage(pkg: TrackedPackage, description: string) {
    const updated = await renamePackage(pkg.id, description)
    setPackages((current) => current.map((item) => (item.id === updated.id ? updated : item)))
    setSelectedPackage((current) => current?.id === updated.id ? updated : current)
  }

  async function removePackage(pkg: TrackedPackage) {
    setArchiving(true)
    try {
      await archivePackage(pkg.id)
      setPackages((current) => current.filter((item) => item.id !== pkg.id))
      setSelectedPackage((current) => current?.id === pkg.id ? null : current)
      setArchiveTarget(null)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Could not archive package')
    } finally {
      setArchiving(false)
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
    <div
      className={`app-shell${pullDistance > 0 ? ' pulling' : ''}`}
      style={{ '--pull-distance': `${pullDistance}px` } as React.CSSProperties}
      onTouchStart={startPull}
      onTouchMove={movePull}
      onTouchEnd={() => finishPull()}
      onTouchCancel={() => finishPull(true)}
    >
      <div className={`pull-indicator${pullDistance >= 56 ? ' pull-ready' : ''}${refreshing ? ' refreshing' : ''}`} role="status" aria-live="polite">
        <RefreshIcon />
        <span>{refreshing ? 'Refreshing tracking' : pullDistance >= 56 ? 'Release to refresh' : 'Pull to refresh'}</span>
      </div>
      <header className="topbar">
        <div>
          <p className="eyebrow">Your deliveries</p>
          <h1>Nimotsu</h1>
        </div>
        <div className="topbar-actions">
          <button className={`refresh-button${refreshing ? ' refreshing' : ''}`} type="button" disabled={loading || refreshing || packages.length === 0} onClick={() => void updateTracking()}>
            <RefreshIcon />
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
          <button className="icon-button" type="button" aria-label="Open settings" onClick={() => setSheet('settings')}>
            <SettingsIcon />
          </button>
        </div>
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
                    <button className="package-card-main package-open" type="button" onClick={() => setSelectedPackage(pkg)} aria-label={`View details for ${pkg.description}`}>
                      <div className="status-mark"><PackageIcon /></div>
                      <div className="package-copy">
                        <div className="package-heading">
                          <h2>{pkg.description}</h2>
                          <span className="status-pill">{formatStatus(pkg.status)}</span>
                        </div>
                        <p className="latest-message">
                          {pkg.latest_message || pkg.tracking_error || 'Waiting for a tracking update'}
                          {pkg.latest_location && <span className="latest-location"><LocationIcon />{pkg.latest_location}</span>}
                        </p>
                        <div className="package-meta">
                          <span>{pkg.tracking_number}</span>
                          {pkg.carrier && <><span aria-hidden="true">·</span><span>{formatCarrier(pkg.carrier)}</span></>}
                          {pkg.estimated_delivery_at && <><span aria-hidden="true">·</span><span>ETA {formatEstimatedDelivery(pkg.estimated_delivery_at)}</span></>}
                          <span className="package-update"><span aria-hidden="true">·</span> {formatRelativeDate(pkg.last_event_at)}</span>
                        </div>
                      </div>
                      <ChevronIcon />
                    </button>
                    <div className="card-actions">
                      <button type="button" onClick={() => void toggleNotifications(pkg)} aria-pressed={pkg.notifications_enabled}>
                        <BellIcon enabled={pkg.notifications_enabled} />
                        {notificationLabel(pkg)}
                      </button>
                      <button className="danger-action" type="button" onClick={() => setArchiveTarget(pkg)}>Archive</button>
                    </div>
                  </article>
                ))}
              </section>
            )}
          </>
        )}
      </main>

      {packages.length > 0 && !sheet && !selectedPackage && !archiveTarget && (
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
      {selectedPackage && (
        <PackageDetailSheet
          pkg={selectedPackage}
          onClose={() => setSelectedPackage(null)}
          onRename={(description) => renameTrackedPackage(selectedPackage, description)}
          onUpdateNotifications={(settings) => updatePackageNotifications(selectedPackage, settings)}
          onArchive={() => setArchiveTarget(selectedPackage)}
        />
      )}
      {archiveTarget && (
        <ConfirmDialog
          title="Archive package?"
          message={`“${archiveTarget.description}” will be removed from your active deliveries. You can add the tracking number again later.`}
          confirmLabel={archiving ? 'Archiving…' : 'Archive package'}
          busy={archiving}
          onCancel={() => setArchiveTarget(null)}
          onConfirm={() => void removePackage(archiveTarget)}
        />
      )}
    </div>
  )
}
