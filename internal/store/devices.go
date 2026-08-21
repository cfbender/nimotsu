package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) RegisterDevice(ctx context.Context, token, platform string) error {
	now := time.Now().UTC().Unix()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO devices (token, platform, created_at, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(token) DO UPDATE SET platform = excluded.platform, updated_at = excluded.updated_at`,
		token, platform, now, now,
	)
	if err != nil {
		return fmt.Errorf("register device: %w", err)
	}
	return nil
}

func (s *Store) ListDeviceTokens(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT token FROM devices ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	tokens := make([]string, 0)
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}
