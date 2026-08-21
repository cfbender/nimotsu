package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) SaveGmailConnection(ctx context.Context, email string, token []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Gmail connection update: %w", err)
	}
	defer tx.Rollback()

	var currentEmail string
	err = tx.QueryRowContext(ctx, `SELECT email FROM gmail_connection WHERE id = 1`).Scan(&currentEmail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read Gmail connection: %w", err)
	}
	if err == nil && currentEmail != email {
		if _, err := tx.ExecContext(ctx, `DELETE FROM email_candidates`); err != nil {
			return fmt.Errorf("clear previous Gmail candidates: %w", err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second).Unix()
	_, err = tx.ExecContext(ctx, `
INSERT INTO gmail_connection (id, email, token, connected_at, updated_at)
VALUES (1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    email = excluded.email,
    token = excluded.token,
    sync_error = '',
    connected_at = excluded.connected_at,
    updated_at = excluded.updated_at`, email, token, now, now)
	if err != nil {
		return fmt.Errorf("save Gmail connection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Gmail connection: %w", err)
	}
	return nil
}

func (s *Store) GmailConnection(ctx context.Context) (GmailConnection, error) {
	var connection GmailConnection
	var lastSync sql.NullInt64
	var connected, updated int64
	err := s.db.QueryRowContext(ctx, `
SELECT email, token, last_sync_at, sync_error, connected_at, updated_at
FROM gmail_connection WHERE id = 1`).Scan(
		&connection.Email, &connection.Token, &lastSync, &connection.SyncError, &connected, &updated,
	)
	if err != nil {
		return GmailConnection{}, err
	}
	if lastSync.Valid {
		value := time.Unix(lastSync.Int64, 0).UTC()
		connection.LastSyncAt = &value
	}
	connection.ConnectedAt = time.Unix(connected, 0).UTC()
	connection.UpdatedAt = time.Unix(updated, 0).UTC()
	return connection, nil
}

func (s *Store) UpdateGmailToken(ctx context.Context, token []byte) error {
	result, err := s.db.ExecContext(ctx, `UPDATE gmail_connection SET token = ?, updated_at = ? WHERE id = 1`, token, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("update Gmail token: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) UpdateGmailSync(ctx context.Context, syncedAt *time.Time, syncError string) error {
	var syncUnix any
	if syncedAt != nil {
		syncUnix = syncedAt.UTC().Unix()
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE gmail_connection
SET last_sync_at = COALESCE(?, last_sync_at), sync_error = ?, updated_at = ?
WHERE id = 1`, syncUnix, syncError, time.Now().UTC().Unix())
	if err != nil {
		return fmt.Errorf("update Gmail sync: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteGmailConnection(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Gmail disconnect: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM email_candidates`); err != nil {
		return fmt.Errorf("delete Gmail candidates: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gmail_connection WHERE id = 1`); err != nil {
		return fmt.Errorf("delete Gmail connection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Gmail disconnect: %w", err)
	}
	return nil
}
