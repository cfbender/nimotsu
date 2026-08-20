package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/nimotsu/internal/store"
	"golang.org/x/oauth2"
)

func TestExtractTrackingNumbers(t *testing.T) {
	text := `
Your UPS package is on the way. 1Z999AA10123456784
Tracking number: 123456789012
Order number: 88888888
USPS: 9400111899223856928499
Again: 1Z999AA10123456784
`
	want := []string{"123456789012", "1Z999AA10123456784", "9400111899223856928499"}
	if got := ExtractTrackingNumbers(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractTrackingNumbers() = %v, want %v", got, want)
	}
}

func TestGmailSyncStoresEncryptedCandidates(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("maxResults") != "50" {
			t.Errorf("maxResults = %q", r.URL.Query().Get("maxResults"))
		}
		if !strings.Contains(r.URL.Query().Get("q"), "tracking") {
			t.Errorf("q = %q", r.URL.Query().Get("q"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]string{{"id": "message-1"}}})
	})
	mux.HandleFunc("/gmail/v1/users/me/messages/message-1", func(w http.ResponseWriter, _ *http.Request) {
		body := base64.RawURLEncoding.EncodeToString([]byte("Tracking number: 1Z999AA10123456784"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           "message-1",
			"internalDate": "1787227200000",
			"payload": map[string]any{
				"mimeType": "text/plain",
				"headers": []map[string]string{
					{"name": "Subject", "value": "Your headphones have shipped"},
					{"name": "From", "value": "Shop <orders@example.com>"},
				},
				"body": map[string]string{"data": body},
			},
		})
	})
	googleAPI := httptest.NewServer(mux)
	defer googleAPI.Close()

	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	service, err := New(Config{
		ClientID: "client-id", ClientSecret: "client-secret", PublicURL: googleAPI.URL, EncryptionKey: key,
	}, dataStore, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	service.gmailAPIURL = googleAPI.URL + "/gmail/v1"
	token := &oauth2.Token{
		AccessToken: "test-access-token", RefreshToken: "test-refresh-token", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour),
	}
	encrypted, err := service.encryptToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), token.RefreshToken) {
		t.Fatal("encrypted token contains plaintext refresh token")
	}
	if err := dataStore.SaveGmailConnection(context.Background(), "person@example.com", encrypted); err != nil {
		t.Fatal(err)
	}

	if err := service.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	candidates, err := dataStore.ListEmailCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].TrackingNumber != "1Z999AA10123456784" || candidates[0].Description != "Your headphones have shipped" {
		t.Fatalf("candidates = %+v", candidates)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Connected || status.CandidateCount != 1 || status.LastSyncAt == nil {
		t.Fatalf("status = %+v", status)
	}
}

func TestAuthURLUsesOfflineAccessAndOneTimeState(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	service, err := New(Config{
		ClientID: "client-id", ClientSecret: "client-secret", PublicURL: "https://packages.example.com", EncryptionKey: key,
	}, dataStore, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := service.AuthURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("access_type") != "offline" || parsed.Query().Get("state") == "" || parsed.Query().Get("scope") != gmailScope {
		t.Fatalf("authorization query = %v", parsed.Query())
	}
	state := parsed.Query().Get("state")
	if err := service.CompleteOAuth(context.Background(), state, ""); err == ErrInvalidState {
		t.Fatal("fresh OAuth state was rejected")
	}
	if err := service.CompleteOAuth(context.Background(), state, ""); err != ErrInvalidState {
		t.Fatalf("reused OAuth state error = %v, want ErrInvalidState", err)
	}
}

func TestNewRejectsInvalidEncryptionKey(t *testing.T) {
	_, err := New(Config{
		ClientID: "client-id", ClientSecret: "client-secret", PublicURL: "https://packages.example.com", EncryptionKey: "not-a-key",
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "base64-encoded 32-byte key") {
		t.Fatalf("New() error = %v", err)
	}
}
