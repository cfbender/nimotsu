package shippo

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cfbender/nimotsu/internal/tracking"
)

const defaultBaseURL = "https://api.goshippo.com"

var (
	upsNumber        = regexp.MustCompile(`^1Z[0-9A-Z]{16}$`)
	uspsNumber       = regexp.MustCompile(`^(?:9[2345][0-9]{18,20}|[A-Z]{2}[0-9]{9}US)$`)
	fedExNumber      = regexp.MustCompile(`^(?:[0-9]{12}|[0-9]{15})$`)
	dhlExpressNumber = regexp.MustCompile(`^[0-9]{10}$`)
	shippoTestNumber = regexp.MustCompile(`^SHIPPO_(?:PRE_TRANSIT|TRANSIT|DELIVERED|RETURNED|FAILURE|UNKNOWN)$`)
)

type Client struct {
	apiToken     string
	webhookToken string
	baseURL      string
	httpClient   *http.Client
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return "Shippo error: " + e.Message
	}
	return fmt.Sprintf("Shippo returned HTTP %d", e.StatusCode)
}

func New(apiToken, webhookToken string) *Client {
	return &Client{
		apiToken:     apiToken,
		webhookToken: webhookToken,
		baseURL:      defaultBaseURL,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Name() string {
	return "shippo"
}

func (c *Client) DetectCarrier(trackingNumber string) string {
	return detectCarrier(trackingNumber)
}

func (c *Client) Register(ctx context.Context, trackingNumber, carrier string) (tracking.Registration, error) {
	carrier = normalizeCarrier(carrier)
	if carrier == "" {
		carrier = detectCarrier(trackingNumber)
	}
	if carrier == "" {
		return tracking.Registration{}, tracking.ErrCarrierRequired
	}
	payload := struct {
		Carrier        string `json:"carrier"`
		TrackingNumber string `json:"tracking_number"`
		Metadata       string `json:"metadata"`
	}{
		Carrier:        carrier,
		TrackingNumber: trackingNumber,
		Metadata:       "Nimotsu",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return tracking.Registration{}, fmt.Errorf("encode Shippo tracking registration: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/tracks/", bytes.NewReader(body))
	if err != nil {
		return tracking.Registration{}, fmt.Errorf("create Shippo tracking request: %w", err)
	}
	request.Header.Set("Authorization", "ShippoToken "+c.apiToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Shippo-API-Version", "2018-02-08")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return tracking.Registration{}, fmt.Errorf("register Shippo tracking: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return tracking.Registration{}, fmt.Errorf("read Shippo tracking registration: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return tracking.Registration{}, decodeAPIError(response.StatusCode, responseBody)
	}
	return decodeRegistration(responseBody)
}

func (c *Client) Lookup(ctx context.Context, trackingNumber, carrier string) (tracking.Registration, error) {
	carrier = normalizeCarrier(carrier)
	if carrier == "" {
		carrier = detectCarrier(trackingNumber)
	}
	if carrier == "" {
		return tracking.Registration{}, tracking.ErrCarrierRequired
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + "/tracks/" + url.PathEscape(carrier) + "/" + url.PathEscape(trackingNumber)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return tracking.Registration{}, fmt.Errorf("create Shippo tracking lookup: %w", err)
	}
	request.Header.Set("Authorization", "ShippoToken "+c.apiToken)
	request.Header.Set("Shippo-API-Version", "2018-02-08")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return tracking.Registration{}, fmt.Errorf("look up Shippo tracking: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return tracking.Registration{}, fmt.Errorf("read Shippo tracking lookup: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return tracking.Registration{}, decodeAPIError(response.StatusCode, responseBody)
	}
	return decodeRegistration(responseBody)
}

func decodeRegistration(responseBody []byte) (tracking.Registration, error) {
	var track trackPayload
	if err := json.Unmarshal(responseBody, &track); err != nil {
		return tracking.Registration{}, fmt.Errorf("decode Shippo tracking response: %w", err)
	}
	if track.TrackingNumber == "" || track.Carrier == "" {
		return tracking.Registration{}, errors.New("Shippo returned incomplete tracking data")
	}
	update := track.update()
	return tracking.Registration{
		ProviderID: normalizeCarrier(track.Carrier) + ":" + strings.ToUpper(track.TrackingNumber),
		History:    track.history(),
		Update:     update,
	}, nil
}

func (c *Client) AuthenticateWebhook(request *http.Request) error {
	if c.webhookToken == "" {
		return tracking.ErrWebhookNotConfigured
	}
	provided := request.URL.Query().Get("token")
	if len(provided) != len(c.webhookToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(c.webhookToken)) != 1 {
		return tracking.ErrWebhookAuthentication
	}
	return nil
}

func (c *Client) ParseWebhook(_ *http.Request, body []byte) (tracking.Update, error) {
	var webhook struct {
		Event string       `json:"event"`
		Data  trackPayload `json:"data"`
	}
	if err := json.Unmarshal(body, &webhook); err != nil {
		return tracking.Update{}, fmt.Errorf("%w: malformed JSON", tracking.ErrInvalidWebhook)
	}
	if webhook.Event != "track_updated" {
		return tracking.Update{}, tracking.ErrIgnoredWebhook
	}
	if webhook.Data.TrackingNumber == "" || webhook.Data.Carrier == "" || webhook.Data.TrackingStatus == nil {
		return tracking.Update{}, fmt.Errorf("%w: missing tracking data", tracking.ErrInvalidWebhook)
	}
	return webhook.Data.update(), nil
}

func normalizeCarrier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == ' ' || character == '-' || character == '_'
	})
	value = strings.Join(parts, "_")
	switch value {
	case "united_states_postal_service":
		return "usps"
	case "united_parcel_service":
		return "ups"
	case "federal_express":
		return "fedex"
	case "dhl", "dhlexpress":
		return "dhl_express"
	case "on_trac":
		return "ontrac"
	default:
		return value
	}
}

func detectCarrier(trackingNumber string) string {
	trackingNumber = strings.ToUpper(strings.TrimSpace(trackingNumber))
	switch {
	case shippoTestNumber.MatchString(trackingNumber):
		return "shippo"
	case upsNumber.MatchString(trackingNumber):
		return "ups"
	case uspsNumber.MatchString(trackingNumber):
		return "usps"
	case fedExNumber.MatchString(trackingNumber):
		return "fedex"
	case dhlExpressNumber.MatchString(trackingNumber):
		return "dhl_express"
	default:
		return ""
	}
}

func decodeAPIError(statusCode int, body []byte) *APIError {
	apiError := &APIError{StatusCode: statusCode}
	var envelope map[string]any
	if json.Unmarshal(body, &envelope) != nil {
		return apiError
	}
	for _, key := range []string{"detail", "message", "error", "non_field_errors"} {
		if message := errorMessage(envelope[key]); message != "" {
			apiError.Message = message
			break
		}
	}
	return apiError
}

func errorMessage(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []any:
		messages := make([]string, 0, len(value))
		for _, item := range value {
			if message := errorMessage(item); message != "" {
				messages = append(messages, message)
			}
		}
		return strings.Join(messages, "; ")
	case map[string]any:
		messages := make([]string, 0, len(value))
		for field, item := range value {
			if message := errorMessage(item); message != "" {
				messages = append(messages, field+": "+message)
			}
		}
		return strings.Join(messages, "; ")
	default:
		return ""
	}
}

type trackPayload struct {
	Carrier         string           `json:"carrier"`
	TrackingNumber  string           `json:"tracking_number"`
	ETA             string           `json:"eta"`
	TrackingStatus  *trackingStatus  `json:"tracking_status"`
	TrackingHistory []trackingStatus `json:"tracking_history"`
}

type trackingStatus struct {
	Status        string             `json:"status"`
	StatusDetails string             `json:"status_details"`
	StatusDate    string             `json:"status_date"`
	Substatus     *trackingSubstatus `json:"substatus"`
	Location      *trackingLocation  `json:"location"`
}

type trackingSubstatus struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

type trackingLocation struct {
	City    string `json:"city"`
	State   string `json:"state"`
	ZIP     string `json:"zip"`
	Country string `json:"country"`
}

func (t trackPayload) update() tracking.Update {
	latest := t.TrackingStatus
	if latest == nil && len(t.TrackingHistory) > 0 {
		latest = &t.TrackingHistory[len(t.TrackingHistory)-1]
	}
	return t.updateFrom(latest)
}

func (t trackPayload) history() []tracking.Update {
	updates := make([]tracking.Update, 0, len(t.TrackingHistory))
	for index := range t.TrackingHistory {
		updates = append(updates, t.updateFrom(&t.TrackingHistory[index]))
	}
	return updates
}

func (t trackPayload) updateFrom(latest *trackingStatus) tracking.Update {
	update := tracking.Update{
		TrackingNumber: strings.ToUpper(t.TrackingNumber),
		Carrier:        normalizeCarrier(t.Carrier),
		Status:         "Unknown",
	}
	if estimatedDeliveryAt, ok := parseTime(t.ETA); ok {
		update.EstimatedDeliveryAt = &estimatedDeliveryAt
	}
	if latest == nil {
		return update
	}
	update.Status = canonicalStatus(latest.Status, latest.Substatus)
	update.LatestMessage = latest.StatusDetails
	update.Location = formatTrackingLocation(latest.Location)
	if latest.Substatus != nil {
		update.SubStatus = pascalCase(latest.Substatus.Code)
		if update.LatestMessage == "" {
			update.LatestMessage = latest.Substatus.Text
		}
	}
	if occurredAt, ok := parseTime(latest.StatusDate); ok {
		update.LastEventAt = &occurredAt
	}
	return update
}

func formatTrackingLocation(location *trackingLocation) string {
	if location == nil {
		return ""
	}
	region := strings.TrimSpace(strings.TrimSpace(location.State) + " " + strings.TrimSpace(location.ZIP))
	parts := make([]string, 0, 3)
	for _, part := range []string{location.City, region, location.Country} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

func canonicalStatus(status string, substatus *trackingSubstatus) string {
	if substatus != nil {
		switch strings.ToLower(substatus.Code) {
		case "out_for_delivery":
			return "OutForDelivery"
		case "available_for_pickup", "pickup_available":
			return "AvailableForPickup"
		case "return_to_sender":
			return "ReturnToSender"
		}
	}
	switch strings.ToUpper(status) {
	case "UNKNOWN":
		return "Unknown"
	case "PRE_TRANSIT":
		return "PreTransit"
	case "TRANSIT":
		return "InTransit"
	case "DELIVERED":
		return "Delivered"
	case "RETURNED":
		return "Returned"
	case "FAILURE":
		return "Failure"
	default:
		return pascalCase(status)
	}
}

func parseTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func pascalCase(value string) string {
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == '_' || character == '-' || character == ' '
	})
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
		}
	}
	return strings.Join(parts, "")
}
