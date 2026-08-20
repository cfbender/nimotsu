const labels: Record<string, string> = {
  NotFound: 'Not found',
  InfoReceived: 'Label created',
  InTransit: 'In transit',
  Expired: 'Tracking expired',
  AvailableForPickup: 'Ready for pickup',
  OutForDelivery: 'Out for delivery',
  DeliveryFailure: 'Delivery issue',
  Delivered: 'Delivered',
  Exception: 'Exception',
  Registered: 'Registered',
  Unregistered: 'Saved locally',
  NeedsCarrier: 'Carrier needed',
  RegistrationFailed: 'Registration failed',
}

export function formatStatus(status: string): string {
  return labels[status] ?? (status.replace(/([a-z])([A-Z])/g, '$1 $2') || 'Waiting for update')
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
