package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	webhookAuthErr  error
	webhookErr      error
	registerCalls   []trackingRequest
	lookupCalls     []trackingRequest
}

type trackingRequest struct {
	number  string
	carrier string
}

func (f *fakeProvider) Name() string {
	return "fake"
}

func (f *fakeProvider) DetectCarrier(number string) string {
	if number == "1Z999AA10123456784" {
		return "ups"
	}
	return ""
}

func (f *fakeProvider) Register(_ context.Context, number, carrier string) (tracking.Registration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls = append(f.registerCalls, trackingRequest{number: number, carrier: carrier})
	return f.registration, f.registrationErr
}

func (f *fakeProvider) Lookup(_ context.Context, number, carrier string) (tracking.Registration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookupCalls = append(f.lookupCalls, trackingRequest{number: number, carrier: carrier})
	return f.registration, f.registrationErr
}

func (f *fakeProvider) AuthenticateWebhook(_ *http.Request) error {
	return f.webhookAuthErr
}

func (f *fakeProvider) ParseWebhook(_ *http.Request, _ []byte) (tracking.Update, error) {
	return f.update, f.webhookErr
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.registerCalls)
}

func (f *fakeProvider) lookupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.lookupCalls)
}

func TestCreatePackageWithoutConfiguredTracker(t *testing.T) {
	dataStore := openTestStore(t)
	handler := New(dataStore, nil, nil, "", nil, nil, testLogger())
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

func TestRenamePackage(t *testing.T) {
	dataStore := openTestStore(t)
	handler := New(dataStore, nil, nil, "", nil, nil, testLogger())
	created := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var pkg store.Package
	if err := json.Unmarshal(created.Body.Bytes(), &pkg); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/packages/"+pkg.ID, bytes.NewBufferString(`{"description":"  Studio headphones  "}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", response.Code, response.Body.String())
	}
	var renamed store.Package
	if err := json.Unmarshal(response.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Description != "Studio headphones" {
		t.Fatalf("renamed package = %+v", renamed)
	}

	emptyRequest := httptest.NewRequest(http.MethodPatch, "/api/packages/"+pkg.ID, bytes.NewBufferString(`{"description":"  "}`))
	emptyRequest.Header.Set("Content-Type", "application/json")
	emptyResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyResponse, emptyRequest)
	if emptyResponse.Code != http.StatusBadRequest {
		t.Fatalf("empty rename status = %d, body = %s", emptyResponse.Code, emptyResponse.Body.String())
	}
}

func TestNotificationDefaultsAndPackageCustomization(t *testing.T) {
	dataStore := openTestStore(t)
	handler := New(dataStore, nil, nil, "", nil, nil, testLogger())

	defaultsRequest := httptest.NewRequest(http.MethodPatch, "/api/settings/notifications", bytes.NewBufferString(`{
        "notifications_enabled":true,
        "notify_in_transit":false,
        "notify_out_for_delivery":true,
        "notify_delivered":true,
        "notify_exceptions":false
    }`))
	defaultsRequest.Header.Set("Content-Type", "application/json")
	defaultsResponse := httptest.NewRecorder()
	handler.ServeHTTP(defaultsResponse, defaultsRequest)
	if defaultsResponse.Code != http.StatusOK {
		t.Fatalf("update defaults status = %d, body = %s", defaultsResponse.Code, defaultsResponse.Body.String())
	}

	createdResponse := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created store.Package
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.NotifyInTransit || !created.NotifyOutForDelivery || !created.NotifyDelivered || created.NotifyExceptions {
		t.Fatalf("created package did not inherit defaults: %+v", created.NotificationSettings)
	}

	customizeRequest := httptest.NewRequest(http.MethodPatch, "/api/packages/"+created.ID, bytes.NewBufferString(`{
        "notification_settings":{
            "notifications_enabled":true,
            "notify_in_transit":true,
            "notify_out_for_delivery":false,
            "notify_delivered":false,
            "notify_exceptions":true
        }
    }`))
	customizeRequest.Header.Set("Content-Type", "application/json")
	customizeResponse := httptest.NewRecorder()
	handler.ServeHTTP(customizeResponse, customizeRequest)
	if customizeResponse.Code != http.StatusOK {
		t.Fatalf("customize package status = %d, body = %s", customizeResponse.Code, customizeResponse.Body.String())
	}
	var customized store.Package
	if err := json.Unmarshal(customizeResponse.Body.Bytes(), &customized); err != nil {
		t.Fatal(err)
	}
	if !customized.NotifyInTransit || customized.NotifyOutForDelivery || customized.NotifyDelivered || !customized.NotifyExceptions {
		t.Fatalf("customized package settings = %+v", customized.NotificationSettings)
	}

	getDefaultsRequest := httptest.NewRequest(http.MethodGet, "/api/settings/notifications", nil)
	getDefaultsResponse := httptest.NewRecorder()
	handler.ServeHTTP(getDefaultsResponse, getDefaultsRequest)
	if getDefaultsResponse.Code != http.StatusOK || getDefaultsResponse.Body.String() != defaultsResponse.Body.String() {
		t.Fatalf("get defaults response = %d %s, want %s", getDefaultsResponse.Code, getDefaultsResponse.Body.String(), defaultsResponse.Body.String())
	}
}

func TestCreateAndRestorePackageRegistersWithProvider(t *testing.T) {
	dataStore := openTestStore(t)
	estimatedDeliveryAt := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	provider := &fakeProvider{registration: tracking.Registration{
		ProviderID: "trk_123",
		Update: tracking.Update{
			Carrier:             "USPS",
			Status:              "PreTransit",
			SubStatus:           "LabelCreated",
			LatestMessage:       "Shipping label created",
			EstimatedDeliveryAt: &estimatedDeliveryAt,
		},
	}}
	handler := New(dataStore, provider, nil, "", nil, nil, testLogger())

	created := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"9400110898825022579493","carrier":"USPS"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	if bytes.Contains(created.Body.Bytes(), []byte("tracking_provider")) {
		t.Fatalf("create response exposes provider fields: %s", created.Body.String())
	}
	var original store.Package
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	if original.Carrier != "USPS" || original.Status != "PreTransit" || original.LatestMessage != "Shipping label created" || original.EstimatedDeliveryAt == nil || !original.EstimatedDeliveryAt.Equal(estimatedDeliveryAt) {
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

func TestPackageTrackingEventsAndCarrierDetection(t *testing.T) {
	dataStore := openTestStore(t)
	currentAt := time.Date(2026, 8, 20, 14, 30, 0, 0, time.UTC)
	registeredAt := currentAt.Add(-24 * time.Hour)
	provider := &fakeProvider{registration: tracking.Registration{
		ProviderID: "trk_123",
		Update: tracking.Update{
			Carrier:       "UPS",
			Status:        "InTransit",
			LatestMessage: "Departed facility",
			Location:      "Los Angeles, CA 90001, US",
			LastEventAt:   &currentAt,
		},
		History: []tracking.Update{{
			Status:        "PreTransit",
			LatestMessage: "Label created",
			Location:      "Phoenix, AZ 85001, US",
			LastEventAt:   &registeredAt,
		}},
	}}
	handler := New(dataStore, provider, nil, "", nil, nil, testLogger())

	carrierRequest := httptest.NewRequest(http.MethodGet, "/api/tracking/carrier?tracking_number=1Z999AA10123456784", nil)
	carrierResponse := httptest.NewRecorder()
	handler.ServeHTTP(carrierResponse, carrierRequest)
	if carrierResponse.Code != http.StatusOK || carrierResponse.Body.String() != "{\"carrier\":\"ups\"}\n" {
		t.Fatalf("carrier response = %d %s", carrierResponse.Code, carrierResponse.Body.String())
	}

	created := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var pkg store.Package
	if err := json.Unmarshal(created.Body.Bytes(), &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.LatestLocation != "Los Angeles, CA 90001, US" {
		t.Fatalf("latest location = %q", pkg.LatestLocation)
	}

	eventsRequest := httptest.NewRequest(http.MethodGet, "/api/packages/"+pkg.ID+"/events", nil)
	eventsResponse := httptest.NewRecorder()
	handler.ServeHTTP(eventsResponse, eventsRequest)
	if eventsResponse.Code != http.StatusOK {
		t.Fatalf("events status = %d, body = %s", eventsResponse.Code, eventsResponse.Body.String())
	}
	var events []store.TrackingEvent
	if err := json.Unmarshal(eventsResponse.Body.Bytes(), &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Status != "InTransit" || events[0].Location != "Los Angeles, CA 90001, US" || events[1].Status != "PreTransit" || events[1].Location != "Phoenix, AZ 85001, US" || !events[1].OccurredAt.Equal(registeredAt) {
		t.Fatalf("events = %+v", events)
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/api/packages/missing/events", nil)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing events status = %d, body = %s", missingResponse.Code, missingResponse.Body.String())
	}
}

func TestRefreshTrackingLooksUpPackagesAndSavesHistory(t *testing.T) {
	dataStore := openTestStore(t)
	inTransitAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	provider := &fakeProvider{registration: tracking.Registration{
		ProviderID: "ups:1Z999AA10123456784",
		Update: tracking.Update{
			Carrier: "UPS", Status: "InTransit", LatestMessage: "Out for delivery", LastEventAt: &inTransitAt,
		},
	}}
	handler := New(dataStore, provider, nil, "", nil, nil, testLogger())
	created := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}

	deliveredAt := inTransitAt.Add(2 * time.Hour)
	provider.mu.Lock()
	provider.registration = tracking.Registration{
		ProviderID: "ups:1Z999AA10123456784",
		Update: tracking.Update{
			Carrier: "UPS", Status: "Delivered", LatestMessage: "Delivered", Location: "Front door", LastEventAt: &deliveredAt,
		},
		History: []tracking.Update{{
			Status: "InTransit", LatestMessage: "Out for delivery", LastEventAt: &inTransitAt,
		}},
	}
	provider.mu.Unlock()

	response := postJSON(handler, "/api/packages/refresh", "")
	if response.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Packages  []store.Package `json:"packages"`
		Refreshed int             `json:"refreshed"`
		Failed    int             `json:"failed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Refreshed != 1 || result.Failed != 0 || len(result.Packages) != 1 || result.Packages[0].Status != "Delivered" || result.Packages[0].LatestLocation != "Front door" {
		t.Fatalf("refresh result = %+v", result)
	}
	if provider.lookupCount() != 1 {
		t.Fatalf("lookup calls = %d", provider.lookupCount())
	}
	provider.mu.Lock()
	if provider.lookupCalls[0].number != "1Z999AA10123456784" || provider.lookupCalls[0].carrier != "UPS" {
		t.Fatalf("lookup request = %+v", provider.lookupCalls[0])
	}
	provider.mu.Unlock()
	events, err := dataStore.ListTrackingEvents(t.Context(), result.Packages[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Status != "Delivered" || events[0].Location != "Front door" || events[1].Status != "InTransit" {
		t.Fatalf("events = %+v", events)
	}
}

func TestRefreshTrackingDoesNotRegressANewerStoredUpdate(t *testing.T) {
	dataStore := openTestStore(t)
	newerAt := time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC)
	provider := &fakeProvider{registration: tracking.Registration{
		ProviderID: "ups:1Z999AA10123456784",
		Update: tracking.Update{
			Carrier: "UPS", Status: "InTransit", LatestMessage: "Out for delivery", LastEventAt: &newerAt,
		},
	}}
	handler := New(dataStore, provider, nil, "", nil, nil, testLogger())
	created := postJSON(handler, "/api/packages", `{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}

	olderAt := newerAt.Add(-2 * time.Hour)
	provider.mu.Lock()
	provider.registration = tracking.Registration{
		ProviderID: "ups:1Z999AA10123456784",
		Update: tracking.Update{
			Carrier: "UPS", Status: "PreTransit", LatestMessage: "Label created", LastEventAt: &olderAt,
		},
	}
	provider.mu.Unlock()

	response := postJSON(handler, "/api/packages/refresh", "")
	if response.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Packages []store.Package `json:"packages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Packages) != 1 || result.Packages[0].Status != "InTransit" || result.Packages[0].LatestMessage != "Out for delivery" || result.Packages[0].LastEventAt == nil || !result.Packages[0].LastEventAt.Equal(newerAt) {
		t.Fatalf("refresh regressed package = %+v", result.Packages)
	}
}

func TestRefreshTrackingRequiresConfiguredProvider(t *testing.T) {
	handler := New(openTestStore(t), nil, nil, "", nil, nil, testLogger())
	response := postJSON(handler, "/api/packages/refresh", "")
	if response.Code != http.StatusServiceUnavailable || !bytes.Contains(response.Body.Bytes(), []byte("Shippo is not configured")) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUnconfiguredGmailEndpoints(t *testing.T) {
	dataStore := openTestStore(t)
	handler := New(dataStore, nil, nil, "", nil, nil, testLogger())

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

func TestTestNotificationRequiresFirebase(t *testing.T) {
	dataStore := openTestStore(t)
	handler := New(dataStore, nil, nil, "", nil, nil, testLogger())
	response := postJSON(handler, "/api/notifications/test", "")

	if response.Code != http.StatusServiceUnavailable || !bytes.Contains(response.Body.Bytes(), []byte("Firebase push notifications are not configured")) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestTestNotificationRequiresRegisteredDevice(t *testing.T) {
	dataStore := openTestStore(t)
	called := false
	handler := New(dataStore, nil, nil, "", nil, func(context.Context, []string) (int, error) {
		called = true
		return 0, nil
	}, testLogger())
	response := postJSON(handler, "/api/notifications/test", "")

	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte("enable notifications on a device")) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if called {
		t.Fatal("test push callback was called without a registered device")
	}
}

func TestTestNotificationSendsToRegisteredDevices(t *testing.T) {
	dataStore := openTestStore(t)
	for _, token := range []string{"first-token", "second-token"} {
		if err := dataStore.RegisterDevice(t.Context(), token, "android"); err != nil {
			t.Fatal(err)
		}
	}
	var received []string
	handler := New(dataStore, nil, nil, "", nil, func(_ context.Context, tokens []string) (int, error) {
		received = append(received, tokens...)
		return len(tokens), nil
	}, testLogger())
	response := postJSON(handler, "/api/notifications/test", "")

	if response.Code != http.StatusOK || response.Body.String() != "{\"sent\":2}\n" {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(received) != 2 || received[0] != "first-token" || received[1] != "second-token" {
		t.Fatalf("tokens = %v", received)
	}
}

func TestTestNotificationReportsSendFailure(t *testing.T) {
	dataStore := openTestStore(t)
	if err := dataStore.RegisterDevice(t.Context(), "device-token", "android"); err != nil {
		t.Fatal(err)
	}
	handler := New(dataStore, nil, nil, "", nil, func(context.Context, []string) (int, error) {
		return 0, errors.New("Firebase unavailable")
	}, testLogger())
	response := postJSON(handler, "/api/notifications/test", "")

	if response.Code != http.StatusBadGateway || !bytes.Contains(response.Body.Bytes(), []byte("Firebase did not accept the test notification")) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
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
	handler := New(dataStore, provider, nil, "", nil, nil, testLogger())
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
	handler := New(dataStore, provider, nil, "", func(updated store.Package) { notifications <- updated }, nil, testLogger())

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

func TestTrackingWebhookHonorsPackageNotificationCategories(t *testing.T) {
	dataStore := openTestStore(t)
	pkg, err := dataStore.CreatePackage(t.Context(), store.NewPackage{
		Description: "Headphones", TrackingNumber: "9400110898825022579493", Status: "PreTransit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetPackageNotificationSettings(t.Context(), pkg.ID, store.NotificationSettings{
		NotificationsEnabled: true,
		NotifyDelivered:      true,
	}); err != nil {
		t.Fatal(err)
	}
	eventAt := time.Date(2026, 8, 20, 14, 30, 0, 0, time.UTC)
	provider := &fakeProvider{update: tracking.Update{
		TrackingNumber: pkg.TrackingNumber,
		Carrier:        "USPS",
		Status:         "OutForDelivery",
		LatestMessage:  "Out for delivery",
		LastEventAt:    &eventAt,
	}}
	notifications := make(chan store.Package, 1)
	handler := New(dataStore, provider, nil, "", func(updated store.Package) { notifications <- updated }, nil, testLogger())

	response := postJSON(handler, "/api/webhooks/tracking", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("out-for-delivery status = %d, body = %s", response.Code, response.Body.String())
	}
	select {
	case notification := <-notifications:
		t.Fatalf("disabled category triggered notification: %+v", notification)
	case <-time.After(50 * time.Millisecond):
	}

	deliveredAt := eventAt.Add(time.Hour)
	provider.update.Status = "Delivered"
	provider.update.LatestMessage = "Delivered"
	provider.update.LastEventAt = &deliveredAt
	response = postJSON(handler, "/api/webhooks/tracking", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("delivered status = %d, body = %s", response.Code, response.Body.String())
	}
	select {
	case notification := <-notifications:
		if notification.Status != "Delivered" {
			t.Fatalf("notification = %+v", notification)
		}
	case <-time.After(time.Second):
		t.Fatal("enabled delivery category did not trigger a notification")
	}
}

func TestNotificationTextIncludesTrackingLocation(t *testing.T) {
	title, body := NotificationText(store.Package{
		Description: "Headphones", Status: "InTransit", LatestMessage: "Arrived at facility", LatestLocation: "Raleigh, NC, US",
	})
	if title != "Headphones: In Transit" || body != "Arrived at facility · Raleigh, NC, US" {
		t.Fatalf("notification = %q, %q", title, body)
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
