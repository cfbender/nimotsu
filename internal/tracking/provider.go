package tracking

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var (
	ErrCarrierRequired       = errors.New("carrier is required")
	ErrIgnoredWebhook        = errors.New("webhook event is not a tracking update")
	ErrInvalidWebhook        = errors.New("invalid tracking webhook")
	ErrWebhookAuthentication = errors.New("invalid tracking webhook authentication")
	ErrWebhookNotConfigured  = errors.New("tracking webhooks are not configured")
)

type Update struct {
	TrackingNumber string
	Carrier        string
	Status         string
	SubStatus      string
	LatestMessage  string
	LastEventAt    *time.Time
}

type Registration struct {
	ProviderID string
	Update
}

type Provider interface {
	Name() string
	Register(ctx context.Context, trackingNumber, carrier string) (Registration, error)
	ParseWebhook(request *http.Request, body []byte) (Update, error)
}
