import type { NotificationSettings } from './api'

const notificationChoices: Array<{
  key: Exclude<keyof NotificationSettings, 'notifications_enabled'>
  label: string
  description: string
}> = [
  { key: 'notify_in_transit', label: 'Progress updates', description: 'Carrier scans while the package is moving' },
  { key: 'notify_out_for_delivery', label: 'Out for delivery', description: 'Also includes packages ready for pickup' },
  { key: 'notify_delivered', label: 'Delivered', description: 'When the package reaches its destination' },
  { key: 'notify_exceptions', label: 'Delays and problems', description: 'Exceptions, failed deliveries, and returns' },
]

function withSetting(settings: NotificationSettings, key: keyof NotificationSettings, value: boolean): NotificationSettings {
  return {
    notifications_enabled: key === 'notifications_enabled' ? value : settings.notifications_enabled,
    notify_in_transit: key === 'notify_in_transit' ? value : settings.notify_in_transit,
    notify_out_for_delivery: key === 'notify_out_for_delivery' ? value : settings.notify_out_for_delivery,
    notify_delivered: key === 'notify_delivered' ? value : settings.notify_delivered,
    notify_exceptions: key === 'notify_exceptions' ? value : settings.notify_exceptions,
  }
}

export function NotificationSettingsControls({
  settings,
  disabled = false,
  onChange,
}: {
  settings: NotificationSettings
  disabled?: boolean
  onChange: (settings: NotificationSettings) => void
}) {
  return (
    <div className="notification-controls">
      <label className="notification-toggle notification-master">
        <span>
          <strong>Send notifications</strong>
          <small>Pause or resume all alerts</small>
        </span>
        <input
          type="checkbox"
          checked={settings.notifications_enabled}
          disabled={disabled}
          onChange={(event) => onChange(withSetting(settings, 'notifications_enabled', event.target.checked))}
        />
      </label>
      <fieldset disabled={disabled || !settings.notifications_enabled}>
        <legend>Notify me about</legend>
        {notificationChoices.map((choice) => (
          <label className="notification-choice" key={choice.key}>
            <input
              type="checkbox"
              checked={settings[choice.key]}
              onChange={(event) => onChange(withSetting(settings, choice.key, event.target.checked))}
            />
            <span>
              <strong>{choice.label}</strong>
              <small>{choice.description}</small>
            </span>
          </label>
        ))}
      </fieldset>
    </div>
  )
}
