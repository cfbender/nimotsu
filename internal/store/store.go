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

type NotificationSettings struct {
	NotificationsEnabled bool `json:"notifications_enabled"`
	NotifyInTransit      bool `json:"notify_in_transit"`
	NotifyOutForDelivery bool `json:"notify_out_for_delivery"`
	NotifyDelivered      bool `json:"notify_delivered"`
	NotifyExceptions     bool `json:"notify_exceptions"`
}

func (settings NotificationSettings) Allows(status string) bool {
	if !settings.NotificationsEnabled {
		return false
	}
	switch status {
	case "OutForDelivery", "AvailableForPickup":
		return settings.NotifyOutForDelivery
	case "Delivered":
		return settings.NotifyDelivered
	case "NotFound", "Expired", "DeliveryFailure", "Failure", "Exception", "Error", "ReturnToSender", "Returned", "Cancelled":
		return settings.NotifyExceptions
	default:
		return settings.NotifyInTransit
	}
}

type Package struct {
	ID                  string     `json:"id"`
	Description         string     `json:"description"`
	TrackingNumber      string     `json:"tracking_number"`
	Carrier             string     `json:"carrier"`
	TrackingProvider    string     `json:"-"`
	TrackingProviderID  string     `json:"-"`
	Status              string     `json:"status"`
	SubStatus           string     `json:"sub_status"`
	LatestMessage       string     `json:"latest_message"`
	LatestLocation      string     `json:"latest_location"`
	EstimatedDeliveryAt *time.Time `json:"estimated_delivery_at"`
	LastEventAt         *time.Time `json:"last_event_at"`
	TrackingError       string     `json:"tracking_error"`
	NotificationSettings
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NewPackage struct {
	Description         string
	TrackingNumber      string
	Carrier             string
	TrackingProvider    string
	TrackingProviderID  string
	Status              string
	SubStatus           string
	LatestMessage       string
	LatestLocation      string
	EstimatedDeliveryAt *time.Time
	LastEventAt         *time.Time
	TrackingError       string
	Events              []TrackingUpdate
}

type TrackingUpdate struct {
	Provider            string
	ProviderID          string
	Status              string
	SubStatus           string
	LatestMessage       string
	Location            string
	EstimatedDeliveryAt *time.Time
	LastEventAt         *time.Time
}

type TrackingEvent struct {
	Status     string    `json:"status"`
	SubStatus  string    `json:"sub_status"`
	Message    string    `json:"message"`
	Location   string    `json:"location"`
	OccurredAt time.Time `json:"occurred_at"`
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
	id, description, tracking_number, carrier, tracking_provider, tracking_provider_id, status, sub_status,
	latest_message, latest_location, estimated_delivery_at, last_event_at, tracking_error, notifications_enabled,
	notify_in_transit, notify_out_for_delivery, notify_delivered, notify_exceptions,
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
func (s *Store) Close() error {
	return s.db.Close()
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
	var estimatedDelivery, lastEvent sql.NullInt64
	var notificationsEnabled, notifyInTransit, notifyOutForDelivery, notifyDelivered, notifyExceptions, archived int
	var created, updated int64
	if err := row.Scan(
		&pkg.ID, &pkg.Description, &pkg.TrackingNumber, &pkg.Carrier, &pkg.TrackingProvider, &pkg.TrackingProviderID, &pkg.Status,
		&pkg.SubStatus, &pkg.LatestMessage, &pkg.LatestLocation, &estimatedDelivery, &lastEvent, &pkg.TrackingError,
		&notificationsEnabled, &notifyInTransit, &notifyOutForDelivery, &notifyDelivered, &notifyExceptions,
		&archived, &created, &updated,
	); err != nil {
		return Package{}, err
	}
	if estimatedDelivery.Valid {
		value := time.Unix(estimatedDelivery.Int64, 0).UTC()
		pkg.EstimatedDeliveryAt = &value
	}
	if lastEvent.Valid {
		value := time.Unix(lastEvent.Int64, 0).UTC()
		pkg.LastEventAt = &value
	}
	pkg.NotificationSettings = NotificationSettings{
		NotificationsEnabled: notificationsEnabled != 0,
		NotifyInTransit:      notifyInTransit != 0,
		NotifyOutForDelivery: notifyOutForDelivery != 0,
		NotifyDelivered:      notifyDelivered != 0,
		NotifyExceptions:     notifyExceptions != 0,
	}
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
