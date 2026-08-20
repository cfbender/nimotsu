package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfbender/nimotsu/internal/store"
	"github.com/cfbender/nimotsu/internal/tracking"
)

type reconciliationProvider struct {
	registerCalls int
	lookupCalls   int
}

func (*reconciliationProvider) Name() string {
	return "test-provider"
}

func (*reconciliationProvider) DetectCarrier(string) string {
	return ""
}

func (p *reconciliationProvider) Register(_ context.Context, number, _ string) (tracking.Registration, error) {
	p.registerCalls++
	return reconciliationRegistration(number), nil
}

func (p *reconciliationProvider) Lookup(_ context.Context, number, _ string) (tracking.Registration, error) {
	p.lookupCalls++
	return reconciliationRegistration(number), nil
}

func reconciliationRegistration(number string) tracking.Registration {
	eventAt := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	historyAt := eventAt.Add(-time.Hour)
	return tracking.Registration{ProviderID: "trk_123", Update: tracking.Update{
		TrackingNumber: number,
		Carrier:        "USPS",
		Status:         "InTransit",
		LatestMessage:  "Moving through network",
		Location:       "Los Angeles, CA 90001, US",
		LastEventAt:    &eventAt,
	}, History: []tracking.Update{{Status: "PreTransit", LatestMessage: "Label created", Location: "Phoenix, AZ 85001, US", LastEventAt: &historyAt}}}
}

func (*reconciliationProvider) ParseWebhook(_ *http.Request, _ []byte) (tracking.Update, error) {
	return tracking.Update{}, nil
}

func TestReconcileTrackingRegistersExistingFailure(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if _, err := dataStore.CreatePackage(t.Context(), store.NewPackage{
		Description:    "Headphones",
		TrackingNumber: "9400110898825022579493",
		Status:         "RegistrationFailed",
		TrackingError:  "old provider failed",
	}); err != nil {
		t.Fatal(err)
	}

	provider := &reconciliationProvider{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reconcileTracking(t.Context(), dataStore, provider, logger)
	reconcileTracking(t.Context(), dataStore, provider, logger)
	packages, err := dataStore.ListPackages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Status != "InTransit" || packages[0].Carrier != "USPS" || packages[0].LatestLocation != "Los Angeles, CA 90001, US" || packages[0].TrackingProvider != "test-provider" || packages[0].TrackingProviderID != "trk_123" || packages[0].TrackingError != "" || provider.registerCalls != 1 || provider.lookupCalls != 1 {
		t.Fatalf("reconciled packages = %+v", packages)
	}
	events, err := dataStore.ListTrackingEvents(t.Context(), packages[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Status != "InTransit" || events[0].Location != "Los Angeles, CA 90001, US" || events[1].Status != "PreTransit" || events[1].Location != "Phoenix, AZ 85001, US" {
		t.Fatalf("tracking events = %+v", events)
	}
}
