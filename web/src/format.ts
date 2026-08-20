const labels: Record<string, string> = {
  NotFound: 'Not found',
  InfoReceived: 'Label created',
  Unknown: 'Waiting for update',
  PreTransit: 'Label created',
  InTransit: 'In transit',
  Expired: 'Tracking expired',
  AvailableForPickup: 'Ready for pickup',
  OutForDelivery: 'Out for delivery',
  DeliveryFailure: 'Delivery issue',
  Failure: 'Delivery issue',
  Delivered: 'Delivered',
  Exception: 'Exception',
  Error: 'Tracking error',
  ReturnToSender: 'Returning to sender',
  Returned: 'Returned to sender',
  Cancelled: 'Cancelled',
  Registered: 'Registered',
  Unregistered: 'Saved locally',
  NeedsCarrier: 'Carrier needed',
  RegistrationFailed: 'Registration failed',
}

export function formatStatus(status: string): string {
  return labels[status] ?? (status.replace(/([a-z])([A-Z])/g, '$1 $2') || 'Waiting for update')
}

export function formatCarrier(carrier: string): string {
  const known: Record<string, string> = {
    usps: 'USPS',
    ups: 'UPS',
    fedex: 'FedEx',
    dhl_express: 'DHL Express',
    shippo: 'Shippo',
  }
  return known[carrier.toLowerCase()] ?? carrier.replace(/[_-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase())
}

export function formatRelativeDate(value: string | null): string {
  if (!value) return 'Waiting for the first scan'
  const date = new Date(value)
  const days = Math.round((date.getTime() - Date.now()) / 86_400_000)
  if (Math.abs(days) < 1) return 'Updated today'
  if (days === -1) return 'Updated yesterday'
  if (days < 0 && days > -7) return `Updated ${Math.abs(days)} days ago`
  return `Updated ${date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}`
}
