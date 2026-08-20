package shippo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/nimotsu/internal/tracking"
)

func TestRegisterDetectsCarrierAndCreatesTrackingRegistration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/tracks/" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "ShippoToken test-token" {
			t.Errorf("authorization = %q", authorization)
		}
		if version := request.Header.Get("Shippo-API-Version"); version != "2018-02-08" {
			t.Errorf("API version = %q", version)
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["carrier"] != "usps" || payload["tracking_number"] != "9400110898825022579493" || payload["metadata"] != "Nimotsu" {
			t.Errorf("payload = %+v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "carrier":"usps","tracking_number":"9400110898825022579493",
	"eta":"2026-08-23T00:00:00Z",
  "tracking_status":{"status":"TRANSIT","status_details":"Moving through network","status_date":"2026-08-20T12:00:00Z","substatus":{"code":"departed_facility","text":"Departed facility","action_required":false},"location":{"city":"Los Angeles","state":"CA","zip":"90001","country":"US"}},
  "tracking_history":[{"status":"PRE_TRANSIT","status_details":"Label created","status_date":"2026-08-19T10:00:00Z","substatus":{"code":"information_received","text":"Information received"},"location":{"city":"Phoenix","state":"AZ","zip":"85001","country":"US"}}],"messages":[]
}`))
	}))
	defer server.Close()

	client := New("test-token", "")
	client.baseURL = server.URL
	registration, err := client.Register(context.Background(), "9400110898825022579493", "")
	if err != nil {
		t.Fatal(err)
	}
	if registration.ProviderID != "usps:9400110898825022579493" || registration.Carrier != "usps" || registration.Status != "InTransit" {
		t.Fatalf("registration = %+v", registration)
	}
	if registration.SubStatus != "DepartedFacility" || registration.LatestMessage != "Moving through network" || registration.Location != "Los Angeles, CA 90001, US" {
		t.Fatalf("registration update = %+v", registration.Update)
	}
	if len(registration.History) != 1 || registration.History[0].Status != "PreTransit" || registration.History[0].LatestMessage != "Label created" || registration.History[0].Location != "Phoenix, AZ 85001, US" {
		t.Fatalf("registration history = %+v", registration.History)
	}
	if registration.LastEventAt == nil || !registration.LastEventAt.Equal(time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("last event = %v", registration.LastEventAt)
	}
	if registration.EstimatedDeliveryAt == nil || !registration.EstimatedDeliveryAt.Equal(time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("estimated delivery = %v", registration.EstimatedDeliveryAt)
	}
}

func TestLookupRetrievesExistingTrackingHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/tracks/ups/1Z999AA10123456784" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "ShippoToken test-token" {
			t.Errorf("authorization = %q", authorization)
		}
		_, _ = response.Write([]byte(`{
  "carrier":"ups","tracking_number":"1Z999AA10123456784",
  "tracking_status":{"status":"TRANSIT","status_details":"Arrived at facility","status_date":"2026-08-20T12:00:00Z"},
  "tracking_history":[{"status":"PRE_TRANSIT","status_details":"Label created","status_date":"2026-08-19T10:00:00Z"}],"messages":[]
}`))
	}))
	defer server.Close()

	client := New("test-token", "")
	client.baseURL = server.URL
	registration, err := client.Lookup(t.Context(), "1Z999AA10123456784", "UPS")
	if err != nil {
		t.Fatal(err)
	}
	if registration.Status != "InTransit" || len(registration.History) != 1 || registration.History[0].Status != "PreTransit" {
		t.Fatalf("registration = %+v", registration)
	}
}

func TestRegisterNormalizesRequestedCarrier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["carrier"] != "dhl_express" {
			t.Errorf("carrier = %q", payload["carrier"])
		}
		_, _ = response.Write([]byte(`{"carrier":"dhl_express","tracking_number":"1234567890","tracking_history":[],"messages":[]}`))
	}))
	defer server.Close()

	client := New("test-token", "")
	client.baseURL = server.URL
	if _, err := client.Register(context.Background(), "1234567890", "DHL Express"); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterRequiresAmbiguousCarrier(t *testing.T) {
	client := New("test-token", "")
	if _, err := client.Register(context.Background(), "ABC12345", ""); !errors.Is(err, tracking.ErrCarrierRequired) {
		t.Fatalf("error = %v, want carrier required", err)
	}
}

func TestRegisterReturnsShippoErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"detail":{"tracking_number":["Invalid tracking number"]}}`))
	}))
	defer server.Close()

	client := New("test-token", "")
	client.baseURL = server.URL
	_, err := client.Register(context.Background(), "1Z999AA10123456784", "ups")
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusBadRequest || apiError.Message != "tracking_number: Invalid tracking number" {
		t.Fatalf("API error = %#v, error = %v", apiError, err)
	}
}

func TestParseWebhookAuthenticatesTokenAndNormalizesUpdate(t *testing.T) {
	body := []byte(`{"event":"track_updated","test":false,"data":{"carrier":"usps","tracking_number":"9400110898825022579493","eta":"2026-08-23T00:00:00Z","tracking_status":{"status":"TRANSIT","status_details":"Out for delivery","status_date":"2026-08-20T14:30:00Z","substatus":{"code":"out_for_delivery","text":"Out for delivery","action_required":false},"location":{"city":"Portland","state":"OR","zip":"97201","country":"US"}},"tracking_history":[],"messages":[]}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/tracking?token=webhook-token", strings.NewReader(string(body)))
	client := New("test-token", "webhook-token")

	update, err := client.ParseWebhook(request, body)
	if err != nil {
		t.Fatal(err)
	}
	if update.TrackingNumber != "9400110898825022579493" || update.Carrier != "usps" || update.Status != "OutForDelivery" || update.SubStatus != "OutForDelivery" {
		t.Fatalf("update = %+v", update)
	}
	if update.LatestMessage != "Out for delivery" || update.Location != "Portland, OR 97201, US" || update.LastEventAt == nil {
		t.Fatalf("update details = %+v", update)
	}
	if update.EstimatedDeliveryAt == nil || !update.EstimatedDeliveryAt.Equal(time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("estimated delivery = %v", update.EstimatedDeliveryAt)
	}
}

func TestParseWebhookRejectsWrongToken(t *testing.T) {
	body := []byte(`{"event":"track_updated"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/tracking?token=wrong", strings.NewReader(string(body)))
	client := New("test-token", "webhook-token")
	if _, err := client.ParseWebhook(request, body); !errors.Is(err, tracking.ErrWebhookAuthentication) {
		t.Fatalf("error = %v", err)
	}
}

func TestParseWebhookIgnoresOtherEvents(t *testing.T) {
	body := []byte(`{"event":"transaction_created","data":{}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/tracking?token=webhook-token", strings.NewReader(string(body)))
	client := New("test-token", "webhook-token")
	if _, err := client.ParseWebhook(request, body); !errors.Is(err, tracking.ErrIgnoredWebhook) {
		t.Fatalf("error = %v, want ignored event", err)
	}
}

func TestParseWebhookMapsShippoPickupSubstatus(t *testing.T) {
	body := []byte(`{"event":"track_updated","data":{"carrier":"usps","tracking_number":"9400110898825022579493","tracking_status":{"status":"TRANSIT","status_details":"Ready for pickup","status_date":"2026-08-20T14:30:00Z","substatus":{"code":"pickup_available","text":"Ready for pickup"}}}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/tracking?token=webhook-token", strings.NewReader(string(body)))
	client := New("test-token", "webhook-token")

	update, err := client.ParseWebhook(request, body)
	if err != nil {
		t.Fatal(err)
	}
	if update.Status != "AvailableForPickup" || update.SubStatus != "PickupAvailable" {
		t.Fatalf("update = %+v", update)
	}
}

func TestDetectCarrierUsesOnlyHighConfidenceFormats(t *testing.T) {
	tests := map[string]string{
		"1Z999AA10123456784":     "ups",
		"9400110898825022579493": "usps",
		"EC000000000US":          "usps",
		"123456789012":           "fedex",
		"1234567890":             "dhl_express",
		"SHIPPO_TRANSIT":         "shippo",
		"12345678901234567890":   "",
		"ABC12345":               "",
	}
	for number, expected := range tests {
		client := New("test-token", "")
		if actual := client.DetectCarrier(number); actual != expected {
			t.Errorf("DetectCarrier(%q) = %q, want %q", number, actual, expected)
		}
	}
}
