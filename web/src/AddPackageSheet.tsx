import { FormEvent, useEffect, useState } from 'react'
import { addPackage, detectCarrier, type TrackedPackage } from './api'
import { formatCarrier } from './format'
import { Sheet } from './Sheet'

export function AddPackageSheet({ onClose, onAdded }: { onClose: () => void; onAdded: (pkg: TrackedPackage) => void }) {
  const [description, setDescription] = useState('')
  const [trackingNumber, setTrackingNumber] = useState('')
  const [carrier, setCarrier] = useState('')
  const [carrierSource, setCarrierSource] = useState<'auto' | 'manual'>('auto')
  const [carrierDetection, setCarrierDetection] = useState<'checking' | 'detected' | ''>('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const normalized = trackingNumber.trim()
    if (carrierSource === 'manual') return
    if (normalized.length < 5) {
      setCarrier('')
      setCarrierDetection('')
      return
    }

    let active = true
    setCarrierDetection('checking')
    const timeout = window.setTimeout(() => {
      void detectCarrier(normalized)
        .then((detected) => {
          if (!active) return
          setCarrier(detected)
          setCarrierDetection(detected ? 'detected' : '')
        })
        .catch(() => active && setCarrierDetection(''))
    }, 250)
    return () => {
      active = false
      window.clearTimeout(timeout)
    }
  }, [carrierSource, trackingNumber])

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
          <input required autoCapitalize="characters" autoComplete="off" spellCheck={false} value={trackingNumber} onChange={(event) => setTrackingNumber(event.target.value.toUpperCase().replace(/\s/g, ''))} placeholder="1Z999AA10123456784" />
        </label>
        <label>
          Carrier <span className="optional">optional</span>
          <input
            list="carrier-options"
            value={carrier}
            onChange={(event) => {
              setCarrier(event.target.value)
              setCarrierSource(event.target.value.trim() ? 'manual' : 'auto')
              setCarrierDetection('')
            }}
            placeholder="Detected as you type"
          />
          <datalist id="carrier-options">
            <option value="usps">USPS</option>
            <option value="ups">UPS</option>
            <option value="fedex">FedEx</option>
            <option value="dhl_express">DHL Express</option>
          </datalist>
          <span className={`field-help${carrierDetection === 'detected' ? ' detected' : ''}`} aria-live="polite">
            {carrierDetection === 'checking'
              ? 'Checking common carrier formats…'
              : carrierDetection === 'detected'
                ? `${formatCarrier(carrier)} detected. You can change it if needed.`
                : 'Common carriers are detected automatically, or you can enter a Shippo carrier token.'}
          </span>
        </label>
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="primary-button full-width" disabled={saving} type="submit">{saving ? 'Adding…' : 'Track package'}</button>
      </form>
    </Sheet>
  )
}
