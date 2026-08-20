package seventeentrack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterUsesCarrierDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("17token") != "test-key" {
			t.Errorf("17token = %q", r.Header.Get("17token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[{"number":"1Z999AA10123456784","carrier":100003,"origin":1}],"rejected":[]}}`))
	}))
	defer server.Close()

	client := New("test-key")
	client.baseURL = server.URL
	registration, err := client.Register(context.Background(), "1Z999AA10123456784", nil)
	if err != nil {
		t.Fatal(err)
	}
	if registration.CarrierCode != 100003 {
		t.Fatalf("carrier = %d, want 100003", registration.CarrierCode)
	}
}

func TestRegisterReturnsProviderRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"accepted":[],"rejected":[{"number":"ABCDE12345","error":{"code":-18019903,"message":"Carrier cannot be detected."}}]}}`))
	}))
	defer server.Close()

	client := New("test-key")
	client.baseURL = server.URL
	_, err := client.Register(context.Background(), "ABCDE12345", nil)
	providerError, ok := err.(*APIError)
	if !ok || providerError.Code != -18019903 {
		t.Fatalf("error = %#v, want API error -18019903", err)
	}
}
