package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrDuplicate = errors.New("package already exists")

type Store struct {
	db *sql.DB
}

type Package struct {
	ID                   string     `json:"id"`
	Description          string     `json:"description"`
	TrackingNumber       string     `json:"tracking_number"`
	CarrierCode          *int64     `json:"carrier_code"`
	Status               string     `json:"status"`
	SubStatus            string     `json:"sub_status"`
	LatestMessage        string     `json:"latest_message"`
	LastEventAt          *time.Time `json:"last_event_at"`
	TrackingError        string     `json:"tracking_error"`
	NotificationsEnabled bool       `json:"notifications_enabled"`
	Archived             bool       `json:"archived"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type NewPackage struct {
	Description    string
	TrackingNumber string
	CarrierCode    *int64
	Status         string
	TrackingError  string
}

type TrackingUpdate struct {
	Status        string
	SubStatus     string
	LatestMessage string
	LastEventAt   *time.Time
}

type GmailConnection struct {
	Email       string
	Token       []byte
	LastSyncAt  *time.Time
	SyncError   string
	ConnectedAt time.Time
	UpdatedAt   time.Time
}

type EmailCandidate struct {
	ID             string    `json:"id"`
	MessageID      string    `json:"-"`
	TrackingNumber string    `json:"tracking_number"`
	Description    string    `json:"description"`
	Sender         string    `json:"sender"`
	MessageAt      time.Time `json:"message_at"`
	CreatedAt      time.Time `json:"created_at"`
}

const packageColumns = `
	id, description, tracking_number, carrier_code, status, sub_status,
	latest_message, last_event_at, tracking_error, notifications_enabled,
	archived, created_at, updated_at`

func Open(path string) (*Store, error) {
	if err := ensureParent(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func ensureParent(path string) error {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	return os.MkdirAll(filepath.Dir(path), 0o750)
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS packages (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    tracking_number TEXT NOT NULL UNIQUE,
    carrier_code INTEGER,
    status TEXT NOT NULL,
    sub_status TEXT NOT NULL DEFAULT '',
    latest_message TEXT NOT NULL DEFAULT '',
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
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

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

func (s *Store) CreatePackage(ctx context.Context, input NewPackage) (Package, error) {
	now := time.Now().UTC().Truncate(time.Second)
	pkg := Package{
		ID:                   newID(),
		Description:          input.Description,
		TrackingNumber:       input.TrackingNumber,
		CarrierCode:          input.CarrierCode,
		Status:               input.Status,
		TrackingError:        input.TrackingError,
		NotificationsEnabled: true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	result, err := s.db.ExecContext(ctx, `
INSERT INTO packages (
    id, description, tracking_number, carrier_code, status, tracking_error,
    notifications_enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(tracking_number) DO NOTHING`,
		pkg.ID, pkg.Description, pkg.TrackingNumber, pkg.CarrierCode, pkg.Status,
		pkg.TrackingError, now.Unix(), now.Unix(),
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
	return pkg, nil
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

func (s *Store) UpdateTracking(ctx context.Context, trackingNumber string, carrierCode int64, update TrackingUpdate) (Package, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Package{}, false, fmt.Errorf("begin tracking update: %w", err)
	}
	defer tx.Rollback()

	pkg, err := scanPackage(tx.QueryRowContext(ctx, `SELECT `+packageColumns+` FROM packages WHERE tracking_number = ?`, trackingNumber))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Package{}, false, sql.ErrNoRows
		}
		return Package{}, false, err
	}

	changed := pkg.Status != update.Status || pkg.SubStatus != update.SubStatus || pkg.LatestMessage != update.LatestMessage
	now := time.Now().UTC().Truncate(time.Second)
	if carrierCode != 0 {
		pkg.CarrierCode = &carrierCode
	}
	pkg.Status = update.Status
	pkg.SubStatus = update.SubStatus
	pkg.LatestMessage = update.LatestMessage
	pkg.LastEventAt = update.LastEventAt
	pkg.TrackingError = ""
	pkg.UpdatedAt = now

	var eventUnix any
	if update.LastEventAt != nil {
		eventUnix = update.LastEventAt.Unix()
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE packages SET carrier_code = ?, status = ?, sub_status = ?, latest_message = ?,
    last_event_at = ?, tracking_error = '', updated_at = ? WHERE id = ?`,
		pkg.CarrierCode, pkg.Status, pkg.SubStatus, pkg.LatestMessage, eventUnix, now.Unix(), pkg.ID,
	); err != nil {
		return Package{}, false, fmt.Errorf("update tracking: %w", err)
	}

	if changed {
		occurredAt := now.Unix()
		if update.LastEventAt != nil {
			occurredAt = update.LastEventAt.Unix()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO tracking_events (package_id, status, sub_status, message, occurred_at, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, pkg.ID, pkg.Status, pkg.SubStatus, pkg.LatestMessage, occurredAt, now.Unix()); err != nil {
			return Package{}, false, fmt.Errorf("record tracking event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Package{}, false, fmt.Errorf("commit tracking update: %w", err)
	}
	return pkg, changed, nil
}

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
) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
			candidate.ID, candidate.MessageID, candidate.TrackingNumber, candidate.Description,
			candidate.Sender, candidate.MessageAt.UTC().Unix(), candidate.CreatedAt.UTC().Unix(), now.Unix(),
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

func (s *Store) packageByID(ctx context.Context, id string) (Package, error) {
	pkg, err := scanPackage(s.db.QueryRowContext(ctx, `SELECT `+packageColumns+` FROM packages WHERE id = ?`, id))
	if err != nil {
		return Package{}, err
	}
	return pkg, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanPackage(row scanner) (Package, error) {
	var pkg Package
	var carrier, lastEvent sql.NullInt64
	var notifications, archived int
	var created, updated int64
	if err := row.Scan(
		&pkg.ID, &pkg.Description, &pkg.TrackingNumber, &carrier, &pkg.Status,
		&pkg.SubStatus, &pkg.LatestMessage, &lastEvent, &pkg.TrackingError,
		&notifications, &archived, &created, &updated,
	); err != nil {
		return Package{}, err
	}
	if carrier.Valid {
		pkg.CarrierCode = &carrier.Int64
	}
	if lastEvent.Valid {
		value := time.Unix(lastEvent.Int64, 0).UTC()
		pkg.LastEventAt = &value
	}
	pkg.NotificationsEnabled = notifications != 0
	pkg.Archived = archived != 0
	pkg.CreatedAt = time.Unix(created, 0).UTC()
	pkg.UpdatedAt = time.Unix(updated, 0).UTC()
	return pkg, nil
}

func scanEmailCandidate(row scanner) (EmailCandidate, error) {
	var candidate EmailCandidate
	var messageAt, createdAt int64
	if err := row.Scan(
		&candidate.ID, &candidate.MessageID, &candidate.TrackingNumber, &candidate.Description,
		&candidate.Sender, &messageAt, &createdAt,
	); err != nil {
		return EmailCandidate{}, err
	}
	candidate.MessageAt = time.Unix(messageAt, 0).UTC()
	candidate.CreatedAt = time.Unix(createdAt, 0).UTC()
	return candidate, nil
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
