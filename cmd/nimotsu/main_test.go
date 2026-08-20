package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/cfbender/nimotsu/internal/store"
	"github.com/cfbender/nimotsu/internal/tracking"
)

type reconciliationProvider struct {
	calls int
}

func (*reconciliationProvider) Name() string {
	return "test-provider"
}

func (p *reconciliationProvider) Register(_ context.Context, number, _ string) (tracking.Registration, error) {
	p.calls++
	return tracking.Registration{ProviderID: "trk_123", Update: tracking.Update{
		TrackingNumber: number,
		Carrier:        "USPS",
		Status:         "InTransit",
		LatestMessage:  "Moving through network",
	}}, nil
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
	if len(packages) != 1 || packages[0].Status != "InTransit" || packages[0].Carrier != "USPS" || packages[0].TrackingProvider != "test-provider" || packages[0].TrackingProviderID != "trk_123" || packages[0].TrackingError != "" || provider.calls != 1 {
		t.Fatalf("reconciled packages = %+v", packages)
	}
}
