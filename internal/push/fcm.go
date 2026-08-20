package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/oauth2/google"
)

const firebaseMessagingScope = "https://www.googleapis.com/auth/firebase.messaging"

type Sender struct {
	projectID  string
	httpClient *http.Client
}

func NewSender(ctx context.Context, credentialsPath string) (*Sender, error) {
	credentialsJSON, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read Firebase credentials: %w", err)
	}
	var serviceAccount struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(credentialsJSON, &serviceAccount); err != nil {
		return nil, fmt.Errorf("decode Firebase credentials: %w", err)
	}
	if serviceAccount.ProjectID == "" {
		return nil, fmt.Errorf("Firebase credentials are missing project_id")
	}
	config, err := google.JWTConfigFromJSON(credentialsJSON, firebaseMessagingScope)
	if err != nil {
		return nil, fmt.Errorf("configure Firebase credentials: %w", err)
	}
	return &Sender{projectID: serviceAccount.ProjectID, httpClient: config.Client(ctx)}, nil
}

func (s *Sender) Send(ctx context.Context, token, title, body, packageID string) error {
	payload := map[string]any{
		"message": map[string]any{
			"token": token,
			"notification": map[string]string{
				"title": title,
				"body":  body,
			},
			"data": map[string]string{
				"package_id": packageID,
			},
			"android": map[string]any{
				"priority": "high",
				"notification": map[string]string{
					"channel_id": "tracking_updates",
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Firebase message: %w", err)
	}
	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", url.PathEscape(s.projectID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create Firebase request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	response, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send Firebase message: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("Firebase returned HTTP %d: %s", response.StatusCode, string(responseBody))
	}
	return nil
}
