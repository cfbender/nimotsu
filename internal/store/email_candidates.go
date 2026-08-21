package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) SaveEmailCandidates(ctx context.Context, candidates []EmailCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin email candidate update: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Truncate(time.Second)
	for _, candidate := range candidates {
		if candidate.ID == "" {
			candidate.ID = newID()
		}
		if candidate.CreatedAt.IsZero() {
			candidate.CreatedAt = now
		}
		_, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO email_candidates (
    id, message_id, tracking_number, description, sender, message_at, status, created_at, updated_at
) SELECT ?, ?, ?, ?, ?, ?, 'pending', ?, ?
WHERE NOT EXISTS (
    SELECT 1 FROM packages WHERE tracking_number = ? AND archived = 0
)`,
			candidate.ID, candidate.MessageID, candidate.TrackingNumber, candidate.Description,
			candidate.Sender, candidate.MessageAt.UTC().Unix(), candidate.CreatedAt.UTC().Unix(), now.Unix(), candidate.TrackingNumber,
		)
		if err != nil {
			return fmt.Errorf("save email candidate: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit email candidates: %w", err)
	}
	return nil
}

func (s *Store) ListEmailCandidates(ctx context.Context) ([]EmailCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, message_id, tracking_number, description, sender, message_at, created_at
FROM email_candidates WHERE status = 'pending'
ORDER BY message_at DESC, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list email candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]EmailCandidate, 0)
	for rows.Next() {
		candidate, err := scanEmailCandidate(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list email candidates: %w", err)
	}
	return candidates, nil
}

func (s *Store) EmailCandidate(ctx context.Context, id string) (EmailCandidate, error) {
	return scanEmailCandidate(s.db.QueryRowContext(ctx, `
SELECT id, message_id, tracking_number, description, sender, message_at, created_at
FROM email_candidates WHERE id = ? AND status = 'pending'`, id))
}

func (s *Store) SetEmailCandidateStatus(ctx context.Context, id, status string) error {
	if status != "accepted" && status != "dismissed" {
		return fmt.Errorf("invalid email candidate status %q", status)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE email_candidates SET status = ?, updated_at = ? WHERE id = ? AND status = 'pending'`,
		status, time.Now().UTC().Unix(), id)
	if err != nil {
		return fmt.Errorf("update email candidate: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) CountEmailCandidates(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM email_candidates WHERE status = 'pending'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count email candidates: %w", err)
	}
	return count, nil
}
