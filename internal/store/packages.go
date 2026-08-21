package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) ListPackages(ctx context.Context) ([]Package, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+packageColumns+`
FROM packages WHERE archived = 0 ORDER BY updated_at DESC, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}
	defer rows.Close()

	packages := make([]Package, 0)
	for rows.Next() {
		pkg, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		packages = append(packages, pkg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list packages: %w", err)
	}
	return packages, nil
}

func (s *Store) ListTrackingEvents(ctx context.Context, packageID string) ([]TrackingEvent, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM packages WHERE id = ? AND archived = 0`, packageID).Scan(&exists); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT status, sub_status, message, location, occurred_at
FROM tracking_events
WHERE package_id = ?
ORDER BY occurred_at DESC, id DESC`, packageID)
	if err != nil {
		return nil, fmt.Errorf("list tracking events: %w", err)
	}
	defer rows.Close()

	events := make([]TrackingEvent, 0)
	for rows.Next() {
		var event TrackingEvent
		var occurredAt int64
		if err := rows.Scan(&event.Status, &event.SubStatus, &event.Message, &event.Location, &occurredAt); err != nil {
			return nil, fmt.Errorf("scan tracking event: %w", err)
		}
		event.OccurredAt = time.Unix(occurredAt, 0).UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tracking events: %w", err)
	}
	return events, nil
}

func (s *Store) RecordTrackingEvents(ctx context.Context, packageID string, updates []TrackingUpdate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tracking history update: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM packages WHERE id = ? AND archived = 0`, packageID).Scan(&exists); err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, update := range updates {
		if err := recordTrackingEvent(ctx, tx, packageID, update, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tracking history update: %w", err)
	}
	return nil
}

func (s *Store) CreatePackage(ctx context.Context, input NewPackage) (Package, error) {
	now := time.Now().UTC().Truncate(time.Second)
	pkg := Package{
		ID:                  newID(),
		Description:         input.Description,
		TrackingNumber:      input.TrackingNumber,
		Carrier:             input.Carrier,
		TrackingProvider:    input.TrackingProvider,
		TrackingProviderID:  input.TrackingProviderID,
		Status:              input.Status,
		SubStatus:           input.SubStatus,
		LatestMessage:       input.LatestMessage,
		LatestLocation:      input.LatestLocation,
		EstimatedDeliveryAt: input.EstimatedDeliveryAt,
		LastEventAt:         input.LastEventAt,
		TrackingError:       input.TrackingError,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	var estimatedDeliveryUnix, lastEventUnix any
	if pkg.EstimatedDeliveryAt != nil {
		estimatedDeliveryUnix = pkg.EstimatedDeliveryAt.UTC().Unix()
	}
	if pkg.LastEventAt != nil {
		lastEventUnix = pkg.LastEventAt.UTC().Unix()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Package{}, fmt.Errorf("begin package creation: %w", err)
	}
	defer tx.Rollback()
	pkg.NotificationSettings, err = scanNotificationSettings(tx.QueryRowContext(ctx, `
SELECT notifications_enabled, notify_in_transit, notify_out_for_delivery, notify_delivered, notify_exceptions
FROM notification_defaults WHERE id = 1`))
	if err != nil {
		return Package{}, fmt.Errorf("read notification defaults: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
INSERT INTO packages (
    id, description, tracking_number, carrier, tracking_provider, tracking_provider_id, status, sub_status,
    latest_message, latest_location, estimated_delivery_at, last_event_at, tracking_error, notifications_enabled,
    notify_in_transit, notify_out_for_delivery, notify_delivered, notify_exceptions, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tracking_number) DO UPDATE SET
    description = excluded.description,
    carrier = excluded.carrier,
    tracking_provider = excluded.tracking_provider,
    tracking_provider_id = excluded.tracking_provider_id,
    status = excluded.status,
    sub_status = excluded.sub_status,
    latest_message = excluded.latest_message,
    latest_location = excluded.latest_location,
    estimated_delivery_at = excluded.estimated_delivery_at,
    last_event_at = excluded.last_event_at,
    tracking_error = excluded.tracking_error,
    notifications_enabled = excluded.notifications_enabled,
    notify_in_transit = excluded.notify_in_transit,
    notify_out_for_delivery = excluded.notify_out_for_delivery,
    notify_delivered = excluded.notify_delivered,
    notify_exceptions = excluded.notify_exceptions,
    archived = 0,
    updated_at = excluded.updated_at
WHERE packages.archived = 1`,
		pkg.ID, pkg.Description, pkg.TrackingNumber, pkg.Carrier, pkg.TrackingProvider, pkg.TrackingProviderID, pkg.Status, pkg.SubStatus,
		pkg.LatestMessage, pkg.LatestLocation, estimatedDeliveryUnix, lastEventUnix, pkg.TrackingError,
		pkg.NotificationsEnabled, pkg.NotifyInTransit, pkg.NotifyOutForDelivery, pkg.NotifyDelivered, pkg.NotifyExceptions,
		now.Unix(), now.Unix(),
	)
	if err != nil {
		return Package{}, fmt.Errorf("create package: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Package{}, fmt.Errorf("create package: %w", err)
	}
	if rows == 0 {
		return Package{}, ErrDuplicate
	}
	created, err := scanPackage(tx.QueryRowContext(ctx, `SELECT `+packageColumns+` FROM packages WHERE tracking_number = ?`, input.TrackingNumber))
	if err != nil {
		return Package{}, fmt.Errorf("read created package: %w", err)
	}
	if created.TrackingProvider != "" {
		for _, event := range input.Events {
			if err := recordTrackingEvent(ctx, tx, created.ID, event, now); err != nil {
				return Package{}, err
			}
		}
		if err := recordTrackingEvent(ctx, tx, created.ID, TrackingUpdate{
			Status: created.Status, SubStatus: created.SubStatus, LatestMessage: created.LatestMessage, Location: created.LatestLocation, LastEventAt: created.LastEventAt,
		}, now); err != nil {
			return Package{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Package{}, fmt.Errorf("commit package creation: %w", err)
	}
	return created, nil
}

func (s *Store) SetNotifications(ctx context.Context, id string, enabled bool) (Package, error) {
	now := time.Now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `
UPDATE packages SET notifications_enabled = ?, updated_at = ? WHERE id = ? AND archived = 0`,
		enabled, now.Unix(), id,
	)
	if err != nil {
		return Package{}, fmt.Errorf("update package notifications: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Package{}, sql.ErrNoRows
	}
	return s.packageByID(ctx, id)
}

func (s *Store) RenamePackage(ctx context.Context, id, description string) (Package, error) {
	now := time.Now().UTC().Truncate(time.Second)
	result, err := s.db.ExecContext(ctx, `
UPDATE packages SET description = ?, updated_at = ? WHERE id = ? AND archived = 0`,
		description, now.Unix(), id,
	)
	if err != nil {
		return Package{}, fmt.Errorf("rename package: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return Package{}, sql.ErrNoRows
	}
	return s.packageByID(ctx, id)
}

func (s *Store) ArchivePackage(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE packages SET archived = 1, updated_at = ? WHERE id = ? AND archived = 0`, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("archive package: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetRegistrationError(ctx context.Context, id, carrier, status, trackingError string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE packages SET carrier = ?, tracking_provider = '', tracking_provider_id = '', status = ?, sub_status = '', latest_message = '',
    latest_location = '', estimated_delivery_at = NULL, last_event_at = NULL, tracking_error = ?, updated_at = ?
WHERE id = ? AND archived = 0`, carrier, status, trackingError, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("set package registration error: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateTracking(ctx context.Context, trackingNumber, carrier string, update TrackingUpdate) (Package, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Package{}, false, fmt.Errorf("begin tracking update: %w", err)
	}
	defer tx.Rollback()

	pkg, err := scanPackage(tx.QueryRowContext(ctx, `SELECT `+packageColumns+` FROM packages WHERE tracking_number = ? AND archived = 0`, trackingNumber))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Package{}, false, sql.ErrNoRows
		}
		return Package{}, false, err
	}
	if update.Provider == "" && pkg.LastEventAt != nil && update.LastEventAt != nil && update.LastEventAt.Before(*pkg.LastEventAt) {
		return pkg, false, nil
	}

	eventChanged := false
	if update.LastEventAt != nil {
		eventChanged = pkg.LastEventAt == nil || !pkg.LastEventAt.Equal(*update.LastEventAt)
	} else if update.Provider != "" {
		eventChanged = pkg.LastEventAt != nil
	}
	changed := pkg.Status != update.Status || pkg.SubStatus != update.SubStatus || pkg.LatestMessage != update.LatestMessage || pkg.LatestLocation != update.Location || eventChanged
	now := time.Now().UTC().Truncate(time.Second)
	if carrier != "" {
		pkg.Carrier = carrier
	}
	if update.Provider != "" {
		pkg.TrackingProvider = update.Provider
		pkg.TrackingProviderID = update.ProviderID
	}
	pkg.Status = update.Status
	pkg.SubStatus = update.SubStatus
	pkg.LatestMessage = update.LatestMessage
	pkg.LatestLocation = update.Location
	pkg.EstimatedDeliveryAt = update.EstimatedDeliveryAt
	if update.LastEventAt != nil || update.Provider != "" {
		pkg.LastEventAt = update.LastEventAt
	}
	pkg.TrackingError = ""
	pkg.UpdatedAt = now

	var estimatedDeliveryUnix, eventUnix any
	if pkg.EstimatedDeliveryAt != nil {
		estimatedDeliveryUnix = pkg.EstimatedDeliveryAt.Unix()
	}
	if pkg.LastEventAt != nil {
		eventUnix = pkg.LastEventAt.Unix()
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE packages SET carrier = ?, tracking_provider = ?, tracking_provider_id = ?, status = ?, sub_status = ?, latest_message = ?,
    latest_location = ?, estimated_delivery_at = ?, last_event_at = ?, tracking_error = '', updated_at = ? WHERE id = ?`,
		pkg.Carrier, pkg.TrackingProvider, pkg.TrackingProviderID, pkg.Status, pkg.SubStatus, pkg.LatestMessage, pkg.LatestLocation, estimatedDeliveryUnix, eventUnix, now.Unix(), pkg.ID,
	); err != nil {
		return Package{}, false, fmt.Errorf("update tracking: %w", err)
	}

	if changed {
		if err := recordTrackingEvent(ctx, tx, pkg.ID, TrackingUpdate{
			Status: pkg.Status, SubStatus: pkg.SubStatus, LatestMessage: pkg.LatestMessage, Location: pkg.LatestLocation, LastEventAt: update.LastEventAt,
		}, now); err != nil {
			return Package{}, false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Package{}, false, fmt.Errorf("commit tracking update: %w", err)
	}
	return pkg, changed, nil
}

type queryExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func recordTrackingEvent(ctx context.Context, executor queryExecutor, packageID string, update TrackingUpdate, fallback time.Time) error {
	occurredAt := fallback
	if update.LastEventAt != nil {
		occurredAt = update.LastEventAt.UTC()
	}
	_, err := executor.ExecContext(ctx, `
INSERT INTO tracking_events (package_id, status, sub_status, message, location, occurred_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(package_id, status, sub_status, message, occurred_at) DO UPDATE SET
    location = excluded.location WHERE excluded.location <> ''`,
		packageID, update.Status, update.SubStatus, update.LatestMessage, update.Location, occurredAt.Unix(), fallback.Unix())
	if err != nil {
		return fmt.Errorf("record tracking event: %w", err)
	}
	return nil
}
