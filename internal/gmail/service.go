package gmail

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cfbender/nimotsu/internal/store"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	gmailScope = "https://www.googleapis.com/auth/gmail.readonly"
	gmailAPI   = "https://gmail.googleapis.com/gmail/v1"
)

var ErrInvalidState = errors.New("invalid or expired OAuth state")

type Config struct {
	ClientID      string
	ClientSecret  string
	PublicURL     string
	EncryptionKey string
}

type Status struct {
	Configured     bool       `json:"configured"`
	Connected      bool       `json:"connected"`
	Email          string     `json:"email,omitempty"`
	LastSyncAt     *time.Time `json:"last_sync_at,omitempty"`
	SyncError      string     `json:"sync_error,omitempty"`
	CandidateCount int        `json:"candidate_count"`
}

type Service struct {
	store       *store.Store
	oauth       oauth2.Config
	cipher      tokenCipher
	httpClient  *http.Client
	logger      *slog.Logger
	statesMu    sync.Mutex
	states      map[string]time.Time
	syncMu      sync.Mutex
	gmailAPIURL string
}

func New(configuration Config, dataStore *store.Store, logger *slog.Logger) (*Service, error) {
	if strings.TrimSpace(configuration.ClientID) == "" || strings.TrimSpace(configuration.ClientSecret) == "" {
		return nil, errors.New("Gmail client ID and secret are required")
	}
	publicURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(configuration.PublicURL), "/"))
	if err != nil || publicURL.Scheme == "" || publicURL.Host == "" || publicURL.RawQuery != "" || publicURL.Fragment != "" {
		return nil, errors.New("Gmail public URL must be an absolute URL without a query or fragment")
	}
	if publicURL.Scheme != "https" && publicURL.Hostname() != "localhost" && publicURL.Hostname() != "127.0.0.1" {
		return nil, errors.New("Gmail public URL must use HTTPS outside localhost")
	}
	tokenCipher, err := newTokenCipher(configuration.EncryptionKey)
	if err != nil {
		return nil, err
	}
	return &Service{
		store: dataStore,
		oauth: oauth2.Config{
			ClientID:     strings.TrimSpace(configuration.ClientID),
			ClientSecret: strings.TrimSpace(configuration.ClientSecret),
			Endpoint:     google.Endpoint,
			RedirectURL:  publicURL.String() + "/api/gmail/oauth/callback",
			Scopes:       []string{gmailScope},
		},
		cipher:      tokenCipher,
		httpClient:  &http.Client{Timeout: 20 * time.Second},
		logger:      logger,
		states:      make(map[string]time.Time),
		gmailAPIURL: gmailAPI,
	}, nil
}

func (s *Service) AuthURL() (string, error) {
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	now := time.Now()
	s.statesMu.Lock()
	for value, expires := range s.states {
		if now.After(expires) {
			delete(s.states, value)
		}
	}
	s.states[state] = now.Add(10 * time.Minute)
	s.statesMu.Unlock()

	return s.oauth.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("include_granted_scopes", "true"),
		oauth2.SetAuthURLParam("prompt", "consent select_account"),
	), nil
}

func (s *Service) CompleteOAuth(ctx context.Context, state, code string) error {
	if !s.consumeState(state) {
		return ErrInvalidState
	}
	if strings.TrimSpace(code) == "" {
		return errors.New("authorization code is missing")
	}
	token, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	if token.RefreshToken == "" {
		return errors.New("Google did not return a refresh token; reconnect and grant offline access")
	}
	client := s.oauth.Client(ctx, token)
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := getJSON(ctx, client, s.gmailAPIURL+"/users/me/profile", &profile); err != nil {
		return fmt.Errorf("read Gmail profile: %w", err)
	}
	if profile.EmailAddress == "" {
		return errors.New("Gmail profile did not include an email address")
	}
	encrypted, err := s.encryptToken(token)
	if err != nil {
		return err
	}
	if err := s.store.SaveGmailConnection(ctx, profile.EmailAddress, encrypted); err != nil {
		return err
	}
	return nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	status := Status{Configured: true}
	connection, err := s.store.GmailConnection(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return Status{}, err
	}
	count, err := s.store.CountEmailCandidates(ctx)
	if err != nil {
		return Status{}, err
	}
	status.Connected = true
	status.Email = connection.Email
	status.LastSyncAt = connection.LastSyncAt
	status.SyncError = connection.SyncError
	status.CandidateCount = count
	return status, nil
}

func (s *Service) Sync(ctx context.Context) (err error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	connection, err := s.store.GmailConnection(ctx)
	if err != nil {
		return err
	}
	syncStarted := time.Now().UTC().Truncate(time.Second)
	defer func() {
		if err != nil {
			_ = s.store.UpdateGmailSync(context.Background(), nil, userFacingSyncError(err))
		}
	}()

	token, err := s.decryptToken(connection.Token)
	if err != nil {
		return err
	}
	tokenSource := s.oauth.TokenSource(ctx, token)
	freshToken, err := tokenSource.Token()
	if err != nil {
		return fmt.Errorf("refresh Gmail access: %w", err)
	}
	if err := s.saveToken(ctx, freshToken); err != nil {
		return err
	}
	client := oauth2.NewClient(ctx, oauth2.ReuseTokenSource(freshToken, tokenSource))

	cutoff := syncStarted.Add(-30 * 24 * time.Hour)
	if connection.LastSyncAt != nil {
		cutoff = connection.LastSyncAt.Add(-24 * time.Hour)
	}
	messages, err := s.listMessages(ctx, client, cutoff)
	if err != nil {
		return err
	}
	candidates := make([]store.EmailCandidate, 0)
	for _, summary := range messages {
		message, err := s.getMessage(ctx, client, summary.ID)
		if err != nil {
			return err
		}
		for _, trackingNumber := range ExtractTrackingNumbers(message.searchableText()) {
			candidates = append(candidates, store.EmailCandidate{
				MessageID:      message.ID,
				TrackingNumber: trackingNumber,
				Description:    message.description(),
				Sender:         message.sender(),
				MessageAt:      message.receivedAt(),
			})
		}
	}
	if err := s.store.SaveEmailCandidates(ctx, candidates); err != nil {
		return err
	}
	if err := s.store.UpdateGmailSync(ctx, &syncStarted, ""); err != nil {
		return err
	}
	return nil
}

func (s *Service) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			err := s.Sync(syncCtx)
			cancel()
			if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, context.Canceled) {
				s.logger.Warn("Gmail sync failed", "error", err)
			}
		}
	}
}

func (s *Service) Disconnect(ctx context.Context) error {
	connection, err := s.store.GmailConnection(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	token, decryptErr := s.decryptToken(connection.Token)
	if err := s.store.DeleteGmailConnection(ctx); err != nil {
		return err
	}
	if decryptErr != nil {
		return decryptErr
	}
	revokeToken := token.RefreshToken
	if revokeToken == "" {
		revokeToken = token.AccessToken
	}
	form := url.Values{"token": []string{revokeToken}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("revoke Google access: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("revoke Google access: status %d", response.StatusCode)
	}
	return nil
}

func (s *Service) consumeState(state string) bool {
	s.statesMu.Lock()
	defer s.statesMu.Unlock()
	expires, ok := s.states[state]
	delete(s.states, state)
	return ok && time.Now().Before(expires)
}

func (s *Service) listMessages(ctx context.Context, client *http.Client, cutoff time.Time) ([]messageSummary, error) {
	query := fmt.Sprintf("after:%d", cutoff.Unix())
	endpoint := s.gmailAPIURL + "/users/me/messages?" + url.Values{
		"maxResults": []string{"50"},
		"q":          []string{query},
	}.Encode()
	var response struct {
		Messages []messageSummary `json:"messages"`
	}
	if err := getJSON(ctx, client, endpoint, &response); err != nil {
		return nil, fmt.Errorf("list Gmail messages: %w", err)
	}
	return response.Messages, nil
}

func (s *Service) getMessage(ctx context.Context, client *http.Client, id string) (gmailMessage, error) {
	var message gmailMessage
	endpoint := s.gmailAPIURL + "/users/me/messages/" + url.PathEscape(id) + "?format=full"
	if err := getJSON(ctx, client, endpoint, &message); err != nil {
		return gmailMessage{}, fmt.Errorf("read Gmail message: %w", err)
	}
	return message, nil
}

func (s *Service) encryptToken(token *oauth2.Token) ([]byte, error) {
	plain, err := json.Marshal(token)
	if err != nil {
		return nil, fmt.Errorf("encode Gmail token: %w", err)
	}
	return s.cipher.encrypt(plain)
}

func (s *Service) decryptToken(encrypted []byte) (*oauth2.Token, error) {
	plain, err := s.cipher.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(plain, &token); err != nil {
		return nil, fmt.Errorf("decode Gmail token: %w", err)
	}
	return &token, nil
}

func (s *Service) saveToken(ctx context.Context, token *oauth2.Token) error {
	encrypted, err := s.encryptToken(token)
	if err != nil {
		return err
	}
	return s.store.UpdateGmailToken(ctx, encrypted)
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("Google API status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(destination); err != nil {
		return err
	}
	return nil
}

func userFacingSyncError(err error) string {
	if strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "401") {
		return "Gmail access expired; reconnect the account"
	}
	return "Could not scan Gmail; try again later"
}
