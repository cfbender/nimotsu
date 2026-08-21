package tracking

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfbender/nimotsu/internal/store"
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

func (p *reconciliationProvider) Register(_ context.Context, number, _ string) (Registration, error) {
	p.registerCalls++
	return reconciliationRegistration(number), nil
}

func (p *reconciliationProvider) Lookup(_ context.Context, number, _ string) (Registration, error) {
	p.lookupCalls++
	return reconciliationRegistration(number), nil
}

func reconciliationRegistration(number string) Registration {
	eventAt := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	historyAt := eventAt.Add(-time.Hour)
	estimatedDeliveryAt := eventAt.Add(72 * time.Hour)
	return Registration{ProviderID: "trk_123", Update: Update{
		TrackingNumber:      number,
		Carrier:             "USPS",
		Status:              "InTransit",
		LatestMessage:       "Moving through network",
		Location:            "Los Angeles, CA 90001, US",
		EstimatedDeliveryAt: &estimatedDeliveryAt,
		LastEventAt:         &eventAt,
	}, History: []Update{{Status: "PreTransit", LatestMessage: "Label created", Location: "Phoenix, AZ 85001, US", LastEventAt: &historyAt}}}
}

func (*reconciliationProvider) AuthenticateWebhook(_ *http.Request) error {
	return nil
}

func (*reconciliationProvider) ParseWebhook(_ *http.Request, _ []byte) (Update, error) {
	return Update{}, nil
}

func TestReconcileSkipsTerminalUnownedPackages(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	if _, err := dataStore.CreatePackage(t.Context(), store.NewPackage{
		Description:    "Old order",
		TrackingNumber: "9400110898825022579493",
		Status:         "Delivered",
	}); err != nil {
		t.Fatal(err)
	}

	provider := &reconciliationProvider{}
	Reconcile(t.Context(), dataStore, provider, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if provider.registerCalls != 0 || provider.lookupCalls != 0 {
		t.Fatalf("provider calls = %d registers, %d lookups, want none", provider.registerCalls, provider.lookupCalls)
	}
}

func TestSaveRegistrationKeepsExistingProviderOwnership(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	created, err := dataStore.CreatePackage(t.Context(), store.NewPackage{
		Description:    "Headphones",
		TrackingNumber: "9400110898825022579493",
		Status:         "Pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	first := reconciliationRegistration(created.TrackingNumber)
	owned, _, err := SaveRegistration(t.Context(), dataStore, "test-provider", created, first)
	if err != nil {
		t.Fatal(err)
	}
	if owned.TrackingProvider != "test-provider" || owned.TrackingProviderID != "trk_123" {
		t.Fatalf("first save ownership = %q/%q", owned.TrackingProvider, owned.TrackingProviderID)
	}

	second := reconciliationRegistration(created.TrackingNumber)
	second.ProviderID = "trk_456"
	updated, _, err := SaveRegistration(t.Context(), dataStore, "test-provider", owned, second)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TrackingProviderID != "trk_123" {
		t.Fatalf("second save replaced provider ID: %q, want trk_123", updated.TrackingProviderID)
	}
}

func TestReconcileRegistersExistingFailure(t *testing.T) {
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
	Reconcile(t.Context(), dataStore, provider, logger)
	Reconcile(t.Context(), dataStore, provider, logger)
	packages, err := dataStore.ListPackages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Status != "InTransit" || packages[0].Carrier != "USPS" || packages[0].LatestLocation != "Los Angeles, CA 90001, US" || packages[0].EstimatedDeliveryAt == nil || packages[0].TrackingProvider != "test-provider" || packages[0].TrackingProviderID != "trk_123" || packages[0].TrackingError != "" || provider.registerCalls != 1 || provider.lookupCalls != 1 {
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
