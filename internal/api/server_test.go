package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfbender/nimotsu/internal/store"
)

func TestValid17TrackSignature(t *testing.T) {
	body := []byte(`{"event":"TRACKING_UPDATED","data":{"number":"RR123456789CN"}}`)
	key := "test-key"
	hash := sha256.Sum256(append(append(append([]byte(nil), body...), '/'), key...))
	signature := hex.EncodeToString(hash[:])

	if !valid17TrackSignature(body, key, signature) {
		t.Fatal("valid signature was rejected")
	}
	if valid17TrackSignature(append(body, ' '), key, signature) {
		t.Fatal("modified body was accepted")
	}
}

func TestCreatePackageWithoutConfiguredTracker(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(dataStore, nil, nil, "", "", nil, logger)
	request := httptest.NewRequest(http.MethodPost, "/api/packages", bytes.NewBufferString(`{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUnconfiguredGmailEndpoints(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	handler := New(dataStore, nil, nil, "", "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

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

func TestAcceptEmailCandidateCreatesPackage(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(dataStore, nil, nil, "", "", nil, logger)
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
	if len(packages) != 1 || packages[0].TrackingNumber != "1Z999AA10123456784" || len(candidates) != 0 {
		t.Fatalf("packages = %+v, candidates = %+v", packages, candidates)
	}
}
