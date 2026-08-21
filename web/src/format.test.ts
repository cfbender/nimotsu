import { describe, expect, it } from 'vitest'
import { formatCarrier, formatEstimatedDelivery, formatStatus, getCarrierTrackingURL } from './format'

describe('formatStatus', () => {
  it('uses friendly tracking labels', () => {
    expect(formatStatus('OutForDelivery')).toBe('Out for delivery')
    expect(formatStatus('PreTransit')).toBe('Label created')
    expect(formatStatus('ReturnToSender')).toBe('Returning to sender')
    expect(formatStatus('Returned')).toBe('Returned to sender')
    expect(formatStatus('NeedsCarrier')).toBe('Carrier needed')
  })

  it('formats an unknown camel-case status', () => {
    expect(formatStatus('CustomStatus')).toBe('Custom Status')
  })

  it('formats Shippo carrier tokens', () => {
    expect(formatCarrier('usps')).toBe('USPS')
    expect(formatCarrier('dhl_express')).toBe('DHL Express')
    expect(formatCarrier('dhl_ecommerce')).toBe('DHL eCommerce')
    expect(formatCarrier('fedex_smartpost')).toBe('FedEx Ground Economy')
    expect(formatCarrier('canada_post')).toBe('Canada Post')
  })

  it('formats estimated delivery dates without shifting UTC midnight', () => {
    expect(formatEstimatedDelivery('2026-08-23T00:00:00Z')).toBe('Sun, Aug 23')
  })
})

describe('getCarrierTrackingURL', () => {
  it('builds an encoded carrier tracking link', () => {
    expect(getCarrierTrackingURL('UPS', '1Z 999')).toBe('https://www.ups.com/track?loc=en_US&tracknum=1Z%20999')
    expect(getCarrierTrackingURL('dhl-express', '1234567890')).toBe('https://www.dhl.com/us-en/home/tracking/tracking-express.html?submit=1&tracking-id=1234567890')
  })

  it('does not link unsupported carriers or empty tracking numbers', () => {
    expect(getCarrierTrackingURL('other_carrier', '123')).toBeNull()
    expect(getCarrierTrackingURL('usps', '  ')).toBeNull()
  })
})
