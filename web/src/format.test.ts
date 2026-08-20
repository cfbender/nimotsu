import { describe, expect, it } from 'vitest'
import { formatStatus } from './format'

describe('formatStatus', () => {
  it('uses friendly tracking labels', () => {
    expect(formatStatus('OutForDelivery')).toBe('Out for delivery')
    expect(formatStatus('NeedsCarrier')).toBe('Carrier needed')
  })

  it('formats an unknown camel-case status', () => {
    expect(formatStatus('CustomStatus')).toBe('Custom Status')
  })
})
