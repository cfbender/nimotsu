package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cfbender/nimotsu/internal/store"
	"github.com/cfbender/nimotsu/internal/tracking"
)

type fakeProvider struct {
	mu              sync.Mutex
	registration    tracking.Registration
	registrationErr error
	update          tracking.Update
	webhookErr      error
	registerCalls   []trackingRequest
}

type trackingRequest struct {
	number  string
	carrier string
}

func (f *fakeProvider) Name() string {
	return "fake"
}

func (f *fakeProvider) Register(_ context.Context, number, carrier string) (tracking.Registration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls = append(f.registerCalls, trackingRequest{number: number, carrier: carrier})
	return f.registration, f.registrationErr
}

func (f *fakeProvider) ParseWebhook(_ *http.Request, _ []byte) (tracking.Update, error) {
	return f.update, f.webhookErr
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.registerCalls)
}

func TestCreatePackageWithoutConfiguredTracker(t *testing.T) {
	dataStore := openTestStore(t)
	handler := New(dataStore, nil, nil, "", nil, testLogger())
	response := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var pkg store.Package
	if err := json.Unmarshal(response.Body.Bytes(), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Status != "Unregistered" || pkg.TrackingError != "Shippo is not configured" {
		t.Fatalf("package = %+v", pkg)
	}
}

func TestCreateAndRestorePackageRegistersWithProvider(t *testing.T) {
	dataStore := openTestStore(t)
	provider := &fakeProvider{registration: tracking.Registration{
		ProviderID: "trk_123",
		Update: tracking.Update{
			Carrier:       "USPS",
			Status:        "PreTransit",
			SubStatus:     "LabelCreated",
			LatestMessage: "Shipping label created",
		},
	}}
	handler := New(dataStore, provider, nil, "", nil, testLogger())

	created := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"9400110898825022579493","carrier":"USPS"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var original store.Package
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	if original.Carrier != "USPS" || original.Status != "PreTransit" || original.LatestMessage != "Shipping label created" {
		t.Fatalf("created package = %+v", original)
	}
	packages, err := dataStore.ListPackages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].TrackingProvider != "fake" || packages[0].TrackingProviderID != "trk_123" {
		t.Fatalf("stored package = %+v", packages)
	}

	archiveRequest := httptest.NewRequest(http.MethodDelete, "/api/packages/"+original.ID, nil)
	archiveResponse := httptest.NewRecorder()
	handler.ServeHTTP(archiveResponse, archiveRequest)
	if archiveResponse.Code != http.StatusNoContent {
		t.Fatalf("archive status = %d", archiveResponse.Code)
	}
	restored := postJSON(handler, "/api/packages", `{"description":"Headphones again","tracking_number":"9400110898825022579493"}`)
	if restored.Code != http.StatusCreated {
		t.Fatalf("restore status = %d, body = %s", restored.Code, restored.Body.String())
	}
	var restoredPackage store.Package
	if err := json.Unmarshal(restored.Body.Bytes(), &restoredPackage); err != nil {
		t.Fatal(err)
	}
	if restoredPackage.ID != original.ID || provider.callCount() != 2 {
		t.Fatalf("restored package = %+v, registrations = %d", restoredPackage, provider.callCount())
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.registerCalls[0].carrier != "USPS" || provider.registerCalls[1].carrier != "" {
		t.Fatalf("registration calls = %+v", provider.registerCalls)
	}
}

func TestUnconfiguredGmailEndpoints(t *testing.T) {
	dataStore := openTestStore(t)
	handler := New(dataStore, nil, nil, "", nil, testLogger())

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/gmail/status", nil)
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || statusResponse.Body.String() != "{\"configured\":false,\"connected\":false,\"candidate_count\":0}\n" {
		t.Fatalf("status = %d, body = %s", statusResponse.Code, statusResponse.Body.String())
	}

	startRequest := httptest.NewRequest(http.MethodPost, "/api/gmail/oauth/start", nil)
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, startRequest)
	if startResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("OAuth start status = %d, body = %s", startResponse.Code, startResponse.Body.String())
	}
}

func TestAcceptEmailCandidateRegistersPackageWithProvider(t *testing.T) {
	dataStore := openTestStore(t)
	if err := dataStore.SaveEmailCandidates(t.Context(), []store.EmailCandidate{{
		ID:             "candidate-1",
		MessageID:      "message-1",
		TrackingNumber: "1Z999AA10123456784",
		Description:    "Your headphones have shipped",
		Sender:         "Shop",
		MessageAt:      time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{registration: tracking.Registration{Update: tracking.Update{Carrier: "UPS", Status: "InTransit"}}}
	handler := New(dataStore, provider, nil, "", nil, testLogger())
	request := httptest.NewRequest(http.MethodPost, "/api/gmail/candidates/candidate-1/accept", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	packages, err := dataStore.ListPackages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := dataStore.ListEmailCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Carrier != "UPS" || packages[0].Status != "InTransit" || len(candidates) != 0 || provider.callCount() != 1 {
		t.Fatalf("packages = %+v, candidates = %+v, registrations = %d", packages, candidates, provider.callCount())
	}
}

func TestTrackingWebhookIsIdempotent(t *testing.T) {
	dataStore := openTestStore(t)
	pkg, err := dataStore.CreatePackage(t.Context(), store.NewPackage{
		Description: "Headphones", TrackingNumber: "9400110898825022579493", Status: "PreTransit",
	})
	if err != nil {
		t.Fatal(err)
	}
	eventAt := time.Date(2026, 8, 20, 14, 30, 0, 0, time.UTC)
	provider := &fakeProvider{update: tracking.Update{
		TrackingNumber: pkg.TrackingNumber,
		Carrier:        "USPS",
		Status:         "OutForDelivery",
		SubStatus:      "OutForDelivery",
		LatestMessage:  "Out for delivery",
		LastEventAt:    &eventAt,
	}}
	notifications := make(chan store.Package, 2)
	handler := New(dataStore, provider, nil, "", func(updated store.Package) { notifications <- updated }, testLogger())

	for delivery := 0; delivery < 2; delivery++ {
		request := httptest.NewRequest(http.MethodPost, "/api/webhooks/tracking", bytes.NewBufferString(`{}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("delivery %d status = %d, body = %s", delivery, response.Code, response.Body.String())
		}
	}
	select {
	case notification := <-notifications:
		if notification.Status != "OutForDelivery" || notification.Carrier != "USPS" {
			t.Fatalf("notification = %+v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("first webhook did not trigger a notification")
	}
	select {
	case notification := <-notifications:
		t.Fatalf("duplicate webhook triggered notification: %+v", notification)
	case <-time.After(50 * time.Millisecond):
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return dataStore
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func postJSON(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
