import { describe, expect, it } from 'vitest'
import { formatCarrier, formatStatus } from './format'

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
    expect(formatCarrier('canada_post')).toBe('Canada Post')
  })
})
