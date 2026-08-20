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
	handler := New(dataStore, nil, "", "", nil, logger)
	request := httptest.NewRequest(http.MethodPost, "/api/packages", bytes.NewBufferString(`{"description":"Headphones","tracking_number":"1Z999AA10123456784"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
