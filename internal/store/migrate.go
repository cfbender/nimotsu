package store

import (
	"context"
	"fmt"
)

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS packages (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    tracking_number TEXT NOT NULL UNIQUE,
    carrier TEXT NOT NULL DEFAULT '',
    tracking_provider TEXT NOT NULL DEFAULT '',
    tracking_provider_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    sub_status TEXT NOT NULL DEFAULT '',
    latest_message TEXT NOT NULL DEFAULT '',
    latest_location TEXT NOT NULL DEFAULT '',
    estimated_delivery_at INTEGER,
    last_event_at INTEGER,
    tracking_error TEXT NOT NULL DEFAULT '',
    notifications_enabled INTEGER NOT NULL DEFAULT 1,
    archived INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tracking_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    sub_status TEXT NOT NULL,
    message TEXT NOT NULL,
    location TEXT NOT NULL DEFAULT '',
    occurred_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(package_id, status, sub_status, message, occurred_at)
);

CREATE TABLE IF NOT EXISTS devices (
    token TEXT PRIMARY KEY,
    platform TEXT NOT NULL CHECK (platform = 'android'),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS gmail_connection (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    email TEXT NOT NULL,
    token BLOB NOT NULL,
    last_sync_at INTEGER,
    sync_error TEXT NOT NULL DEFAULT '',
    connected_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS email_candidates (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL,
    tracking_number TEXT NOT NULL,
    description TEXT NOT NULL,
    sender TEXT NOT NULL DEFAULT '',
    message_at INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'dismissed')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(message_id, tracking_number)
);`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	if err := s.ensurePackageTrackingColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureTrackingEventLocationColumn(ctx); err != nil {
		return err
	}
	if err := s.backfillTrackingEvents(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensurePackageTrackingColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(packages)`)
	if err != nil {
		return fmt.Errorf("inspect packages schema: %w", err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan packages schema: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect packages schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close packages schema inspection: %w", err)
	}
	for _, column := range []string{"carrier", "tracking_provider", "tracking_provider_id", "latest_location"} {
		if columns[column] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE packages ADD COLUMN `+column+` TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add package %s: %w", column, err)
		}
	}
	if !columns["estimated_delivery_at"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE packages ADD COLUMN estimated_delivery_at INTEGER`); err != nil {
			return fmt.Errorf("add package estimated_delivery_at: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureTrackingEventLocationColumn(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('tracking_events') WHERE name = 'location'`).Scan(&count); err != nil {
		return fmt.Errorf("inspect tracking events schema: %w", err)
	}
	if count == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE tracking_events ADD COLUMN location TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add tracking event location: %w", err)
		}
	}
	return nil
}

func (s *Store) backfillTrackingEvents(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO tracking_events (package_id, status, sub_status, message, occurred_at, created_at)
SELECT id, status, sub_status, latest_message, COALESCE(last_event_at, created_at), created_at
FROM packages
WHERE archived = 0 AND tracking_provider <> '' AND status <> ''`)
	if err != nil {
		return fmt.Errorf("backfill tracking events: %w", err)
	}
	return nil
}
