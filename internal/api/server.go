package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cfbender/nimotsu/internal/seventeentrack"
	"github.com/cfbender/nimotsu/internal/store"
)

var trackingNumberPattern = regexp.MustCompile(`^[A-Z0-9-]{5,50}$`)

type Tracker interface {
	Register(ctx context.Context, number string, carrierCode *int64) (seventeentrack.Registration, error)
}

type Server struct {
	store             *store.Store
	tracker           Tracker
	apiToken          string
	seventeenTrackKey string
	onTrackingUpdate  func(store.Package)
	logger            *slog.Logger
}

func New(dataStore *store.Store, trackerClient Tracker, apiToken, seventeenTrackKey string, onTrackingUpdate func(store.Package), logger *slog.Logger) http.Handler {
	server := &Server{
		store:             dataStore,
		tracker:           trackerClient,
		apiToken:          apiToken,
		seventeenTrackKey: seventeenTrackKey,
		onTrackingUpdate:  onTrackingUpdate,
		logger:            logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.Handle("GET /api/packages", server.authorize(http.HandlerFunc(server.listPackages)))
	mux.Handle("POST /api/packages", server.authorize(http.HandlerFunc(server.createPackage)))
	mux.Handle("PATCH /api/packages/{id}", server.authorize(http.HandlerFunc(server.updatePackage)))
	mux.Handle("DELETE /api/packages/{id}", server.authorize(http.HandlerFunc(server.archivePackage)))
	mux.Handle("POST /api/devices", server.authorize(http.HandlerFunc(server.registerDevice)))
	mux.HandleFunc("POST /api/webhooks/17track", server.seventeenTrackWebhook)

	return cors(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listPackages(w http.ResponseWriter, r *http.Request) {
	packages, err := s.store.ListPackages(r.Context())
	if err != nil {
		s.internalError(w, "list packages", err)
		return
	}
	writeJSON(w, http.StatusOK, packages)
}

func (s *Server) createPackage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Description    string `json:"description"`
		TrackingNumber string `json:"tracking_number"`
		CarrierCode    *int64 `json:"carrier_code"`
	}
	if err := readJSON(w, r, &request); err != nil {
		return
	}
	request.Description = strings.TrimSpace(request.Description)
	request.TrackingNumber = strings.ToUpper(strings.TrimSpace(request.TrackingNumber))
	if request.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}
	if !trackingNumberPattern.MatchString(request.TrackingNumber) {
		writeError(w, http.StatusBadRequest, "tracking number must be 5-50 letters, numbers, or hyphens")
		return
	}
	if request.CarrierCode != nil && *request.CarrierCode <= 0 {
		writeError(w, http.StatusBadRequest, "carrier code must be a positive number")
		return
	}

	status := "Unregistered"
	trackingError := "17TRACK is not configured"
	carrierCode := request.CarrierCode
	if s.tracker != nil {
		registration, err := s.tracker.Register(r.Context(), request.TrackingNumber, request.CarrierCode)
		switch {
		case err == nil:
			status = "Registered"
			trackingError = ""
			carrierCode = &registration.CarrierCode
		case is17TrackCode(err, -18019901):
			status = "Registered"
			trackingError = ""
		case is17TrackCode(err, -18019903):
			status = "NeedsCarrier"
			trackingError = err.Error()
		default:
			status = "RegistrationFailed"
			trackingError = err.Error()
			s.logger.Warn("17TRACK registration failed", "tracking_number", request.TrackingNumber, "error", err)
		}
	}

	pkg, err := s.store.CreatePackage(r.Context(), store.NewPackage{
		Description:    request.Description,
		TrackingNumber: request.TrackingNumber,
		CarrierCode:    carrierCode,
		Status:         status,
		TrackingError:  trackingError,
	})
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "that tracking number is already saved")
		return
	}
	if err != nil {
		s.internalError(w, "create package", err)
		return
	}
	writeJSON(w, http.StatusCreated, pkg)
}

func (s *Server) updatePackage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		NotificationsEnabled *bool `json:"notifications_enabled"`
	}
	if err := readJSON(w, r, &request); err != nil {
		return
	}
	if request.NotificationsEnabled == nil {
		writeError(w, http.StatusBadRequest, "notifications_enabled is required")
		return
	}
	pkg, err := s.store.SetNotifications(r.Context(), r.PathValue("id"), *request.NotificationsEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "package not found")
		return
	}
	if err != nil {
		s.internalError(w, "update package", err)
		return
	}
	writeJSON(w, http.StatusOK, pkg)
}

func (s *Server) archivePackage(w http.ResponseWriter, r *http.Request) {
	err := s.store.ArchivePackage(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "package not found")
		return
	}
	if err != nil {
		s.internalError(w, "archive package", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := readJSON(w, r, &request); err != nil {
		return
	}
	request.Token = strings.TrimSpace(request.Token)
	if request.Token == "" {
		writeError(w, http.StatusBadRequest, "device token is required")
		return
	}
	if request.Platform != "android" {
		writeError(w, http.StatusBadRequest, "only the android platform is supported")
		return
	}
	if err := s.store.RegisterDevice(r.Context(), request.Token, request.Platform); err != nil {
		s.internalError(w, "register device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) seventeenTrackWebhook(w http.ResponseWriter, r *http.Request) {
	if s.seventeenTrackKey == "" {
		writeError(w, http.StatusServiceUnavailable, "17TRACK is not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}
	if !valid17TrackSignature(body, s.seventeenTrackKey, r.Header.Get("sign")) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	var webhook trackingWebhook
	if err := json.Unmarshal(body, &webhook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook JSON")
		return
	}
	if webhook.Event != "TRACKING_UPDATED" || webhook.Data.Number == "" {
		writeError(w, http.StatusBadRequest, "unsupported webhook event")
		return
	}

	message := webhook.Data.TrackInfo.LatestEvent.Description
	if translated := webhook.Data.TrackInfo.LatestEvent.DescriptionTranslation.Description; translated != "" {
		message = translated
	}
	var eventAt *time.Time
	if raw := webhook.Data.TrackInfo.LatestEvent.TimeUTC; raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err == nil {
			eventAt = &parsed
		}
	}
	pkg, changed, err := s.store.UpdateTracking(r.Context(), strings.ToUpper(webhook.Data.Number), webhook.Data.Carrier, store.TrackingUpdate{
		Status:        webhook.Data.TrackInfo.LatestStatus.Status,
		SubStatus:     webhook.Data.TrackInfo.LatestStatus.SubStatus,
		LatestMessage: message,
		LastEventAt:   eventAt,
	})
	if errors.Is(err, sql.ErrNoRows) {
		// The 17TRACK account may contain registrations not owned by this instance.
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		s.internalError(w, "process 17TRACK webhook", err)
		return
	}
	w.WriteHeader(http.StatusOK)
	if changed && pkg.NotificationsEnabled && s.onTrackingUpdate != nil {
		go s.onTrackingUpdate(pkg)
	}
}

func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(s.apiToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.apiToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid API token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) internalError(w http.ResponseWriter, operation string, err error) {
	s.logger.Error(operation, "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

type trackingWebhook struct {
	Event string `json:"event"`
	Data  struct {
		Number    string `json:"number"`
		Carrier   int64  `json:"carrier"`
		TrackInfo struct {
			LatestStatus struct {
				Status    string `json:"status"`
				SubStatus string `json:"sub_status"`
			} `json:"latest_status"`
			LatestEvent struct {
				TimeUTC                string `json:"time_utc"`
				Description            string `json:"description"`
				DescriptionTranslation struct {
					Description string `json:"description"`
				} `json:"description_translation"`
			} `json:"latest_event"`
		} `json:"track_info"`
	} `json:"data"`
}

func valid17TrackSignature(body []byte, key, provided string) bool {
	hash := sha256.Sum256(append(append(append([]byte(nil), body...), '/'), key...))
	expected := make([]byte, hex.EncodedLen(len(hash)))
	hex.Encode(expected, hash[:])
	provided = strings.ToLower(strings.TrimSpace(provided))
	return len(provided) == len(expected) && subtle.ConstantTimeCompare([]byte(provided), expected) == 1
}

func is17TrackCode(err error, code int) bool {
	var apiError *seventeentrack.APIError
	return errors.As(err, &apiError) && apiError.Code == code
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "body must contain one JSON value")
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func cors(next http.Handler) http.Handler {
	allowed := map[string]bool{
		"capacitor://localhost": true,
		"http://localhost":      true,
		"https://localhost":     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			if origin != "" && !allowed[origin] {
				writeError(w, http.StatusForbidden, "origin not allowed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func formatStatus(status string) string {
	if status == "" {
		return "Package updated"
	}
	var output strings.Builder
	for index, character := range status {
		if index > 0 && character >= 'A' && character <= 'Z' {
			output.WriteByte(' ')
		}
		output.WriteRune(character)
	}
	return output.String()
}

func NotificationText(pkg store.Package) (string, string) {
	title := pkg.Description + ": " + formatStatus(pkg.Status)
	body := pkg.LatestMessage
	if body == "" {
		body = "Tracking status changed to " + formatStatus(pkg.Status)
	}
	return title, body
}
