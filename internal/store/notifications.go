package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const notificationSettingsColumns = `
	notifications_enabled, notify_in_transit, notify_out_for_delivery, notify_delivered, notify_exceptions`

func (s *Store) NotificationDefaults(ctx context.Context) (NotificationSettings, error) {
	settings, err := scanNotificationSettings(s.db.QueryRowContext(ctx, `
SELECT `+notificationSettingsColumns+` FROM notification_defaults WHERE id = 1`))
	if err != nil {
		return NotificationSettings{}, fmt.Errorf("read notification defaults: %w", err)
	}
	return settings, nil
}

func (s *Store) SetNotificationDefaults(ctx context.Context, settings NotificationSettings) (NotificationSettings, error) {
	_, err := s.db.ExecContext(ctx, `
UPDATE notification_defaults SET notifications_enabled = ?, notify_in_transit = ?, notify_out_for_delivery = ?,
    notify_delivered = ?, notify_exceptions = ?, updated_at = ? WHERE id = 1`,
		settings.NotificationsEnabled, settings.NotifyInTransit, settings.NotifyOutForDelivery,
		settings.NotifyDelivered, settings.NotifyExceptions, time.Now().UTC().Unix(),
	)
	if err != nil {
		return NotificationSettings{}, fmt.Errorf("update notification defaults: %w", err)
	}
	return s.NotificationDefaults(ctx)
}

func (s *Store) SetPackageNotificationSettings(ctx context.Context, id string, settings NotificationSettings) (Package, error) {
	now := time.Now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `
UPDATE packages SET notifications_enabled = ?, notify_in_transit = ?, notify_out_for_delivery = ?,
    notify_delivered = ?, notify_exceptions = ?, updated_at = ? WHERE id = ? AND archived = 0`,
		settings.NotificationsEnabled, settings.NotifyInTransit, settings.NotifyOutForDelivery,
		settings.NotifyDelivered, settings.NotifyExceptions, now.Unix(), id,
	)
	if err != nil {
		return Package{}, fmt.Errorf("update package notification settings: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Package{}, sql.ErrNoRows
	}
	return s.packageByID(ctx, id)
}

func scanNotificationSettings(row scanner) (NotificationSettings, error) {
	var enabled, inTransit, outForDelivery, delivered, exceptions int
	if err := row.Scan(&enabled, &inTransit, &outForDelivery, &delivered, &exceptions); err != nil {
		return NotificationSettings{}, err
	}
	return NotificationSettings{
		NotificationsEnabled: enabled != 0,
		NotifyInTransit:      inTransit != 0,
		NotifyOutForDelivery: outForDelivery != 0,
		NotifyDelivered:      delivered != 0,
		NotifyExceptions:     exceptions != 0,
	}, nil
}
