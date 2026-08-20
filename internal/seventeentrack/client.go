package seventeentrack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.17track.net/track/v2.4"

type Client struct {
	key        string
	baseURL    string
	httpClient *http.Client
}

type Registration struct {
	CarrierCode int64
	Origin      int
}

type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("17TRACK error %d: %s", e.Code, e.Message)
}

func New(key string) *Client {
	return &Client{
		key:     key,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) Register(ctx context.Context, number string, carrierCode *int64) (Registration, error) {
	payload := []registerRequest{{Number: number, Carrier: carrierCode, AutoDetection: true}}
	body, err := json.Marshal(payload)
	if err != nil {
		return Registration{}, fmt.Errorf("encode 17TRACK registration: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/register", bytes.NewReader(body))
	if err != nil {
		return Registration{}, fmt.Errorf("create 17TRACK registration: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("17token", c.key)

	response, err := c.httpClient.Do(req)
	if err != nil {
		return Registration{}, fmt.Errorf("register with 17TRACK: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Registration{}, fmt.Errorf("read 17TRACK registration: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Registration{}, fmt.Errorf("17TRACK returned HTTP %d", response.StatusCode)
	}

	var decoded registerResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return Registration{}, fmt.Errorf("decode 17TRACK registration: %w", err)
	}
	if decoded.Code != 0 {
		return Registration{}, &APIError{Code: decoded.Code, Message: "request rejected"}
	}
	for _, accepted := range decoded.Data.Accepted {
		if strings.EqualFold(accepted.Number, number) {
			return Registration{CarrierCode: accepted.Carrier, Origin: accepted.Origin}, nil
		}
	}
	for _, rejected := range decoded.Data.Rejected {
		if strings.EqualFold(rejected.Number, number) {
			return Registration{}, &APIError{Code: rejected.Error.Code, Message: rejected.Error.Message}
		}
	}
	return Registration{}, fmt.Errorf("17TRACK returned no result for tracking number")
}

type registerRequest struct {
	Number        string `json:"number"`
	Carrier       *int64 `json:"carrier,omitempty"`
	AutoDetection bool   `json:"auto_detection"`
}

type registerResponse struct {
	Code int `json:"code"`
	Data struct {
		Accepted []struct {
			Number  string `json:"number"`
			Carrier int64  `json:"carrier"`
			Origin  int    `json:"origin"`
		} `json:"accepted"`
		Rejected []struct {
			Number string `json:"number"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"rejected"`
	} `json:"data"`
}
