package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cfbender/nimotsu/internal/gmail"
	"github.com/cfbender/nimotsu/internal/store"
	"github.com/cfbender/nimotsu/internal/tracking"
)

var trackingNumberPattern = regexp.MustCompile(`^[A-Z0-9_-]{5,50}$`)

type Server struct {
	store            *store.Store
	tracker          tracking.Provider
	gmail            *gmail.Service
	apiToken         string
	onTrackingUpdate func(store.Package)
	logger           *slog.Logger
}

func New(dataStore *store.Store, trackerClient tracking.Provider, gmailService *gmail.Service, apiToken string, onTrackingUpdate func(store.Package), logger *slog.Logger) http.Handler {
	server := &Server{
		store:            dataStore,
		tracker:          trackerClient,
		gmail:            gmailService,
		apiToken:         apiToken,
		onTrackingUpdate: onTrackingUpdate,
		logger:           logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.Handle("GET /api/packages", server.authorize(http.HandlerFunc(server.listPackages)))
	mux.Handle("POST /api/packages", server.authorize(http.HandlerFunc(server.createPackage)))
	mux.Handle("GET /api/packages/{id}/events", server.authorize(http.HandlerFunc(server.listTrackingEvents)))
	mux.Handle("PATCH /api/packages/{id}", server.authorize(http.HandlerFunc(server.updatePackage)))
	mux.Handle("DELETE /api/packages/{id}", server.authorize(http.HandlerFunc(server.archivePackage)))
	mux.Handle("GET /api/tracking/carrier", server.authorize(http.HandlerFunc(server.detectCarrier)))
	mux.Handle("POST /api/devices", server.authorize(http.HandlerFunc(server.registerDevice)))
	mux.Handle("GET /api/gmail/status", server.authorize(http.HandlerFunc(server.gmailStatus)))
	mux.Handle("POST /api/gmail/oauth/start", server.authorize(http.HandlerFunc(server.startGmailOAuth)))
	mux.HandleFunc("GET /api/gmail/oauth/callback", server.completeGmailOAuth)
	mux.Handle("POST /api/gmail/sync", server.authorize(http.HandlerFunc(server.syncGmail)))
	mux.Handle("DELETE /api/gmail", server.authorize(http.HandlerFunc(server.disconnectGmail)))
	mux.Handle("GET /api/gmail/candidates", server.authorize(http.HandlerFunc(server.listEmailCandidates)))
	mux.Handle("POST /api/gmail/candidates/{id}/accept", server.authorize(http.HandlerFunc(server.acceptEmailCandidate)))
	mux.Handle("DELETE /api/gmail/candidates/{id}", server.authorize(http.HandlerFunc(server.dismissEmailCandidate)))
	mux.HandleFunc("POST /api/webhooks/tracking", server.trackingWebhook)

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

func (s *Server) listTrackingEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListTrackingEvents(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "package not found")
		return
	}
	if err != nil {
		s.internalError(w, "list tracking events", err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) detectCarrier(w http.ResponseWriter, r *http.Request) {
	trackingNumber := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("tracking_number")))
	carrier := ""
	if s.tracker != nil && trackingNumberPattern.MatchString(trackingNumber) {
		carrier = s.tracker.DetectCarrier(trackingNumber)
	}
	writeJSON(w, http.StatusOK, map[string]string{"carrier": carrier})
}

func (s *Server) createPackage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Description    string `json:"description"`
		TrackingNumber string `json:"tracking_number"`
		Carrier        string `json:"carrier"`
	}
	if err := readJSON(w, r, &request); err != nil {
		return
	}
	request.Description = strings.TrimSpace(request.Description)
	request.TrackingNumber = strings.ToUpper(strings.TrimSpace(request.TrackingNumber))
	request.Carrier = strings.TrimSpace(request.Carrier)
	if request.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}
	if !trackingNumberPattern.MatchString(request.TrackingNumber) {
		writeError(w, http.StatusBadRequest, "tracking number must be 5-50 letters, numbers, hyphens, or underscores")
		return
	}
	if len(request.Carrier) > 100 {
		writeError(w, http.StatusBadRequest, "carrier must be 100 characters or fewer")
		return
	}

	pkg, err := s.savePackage(r.Context(), request.Description, request.TrackingNumber, request.Carrier)
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

func (s *Server) savePackage(ctx context.Context, description, trackingNumber, requestedCarrier string) (store.Package, error) {
	status := "Unregistered"
	trackingError := "Shippo is not configured"
	carrier := requestedCarrier
	providerName := ""
	var registration tracking.Registration
	if s.tracker != nil {
		var err error
		registration, err = s.tracker.Register(ctx, trackingNumber, requestedCarrier)
		switch {
		case err == nil:
			providerName = s.tracker.Name()
			status = registration.Status
			if status == "" {
				status = "Registered"
			}
			trackingError = ""
			if registration.Carrier != "" {
				carrier = registration.Carrier
			}
		case errors.Is(err, tracking.ErrCarrierRequired):
			status = "NeedsCarrier"
			trackingError = err.Error()
		default:
			status = "RegistrationFailed"
			trackingError = err.Error()
			s.logger.Warn("tracking provider registration failed", "tracking_number", trackingNumber, "error", err)
		}
	}
	events := make([]store.TrackingUpdate, 0, len(registration.History))
	for _, event := range registration.History {
		events = append(events, store.TrackingUpdate{
			Status:        event.Status,
			SubStatus:     event.SubStatus,
			LatestMessage: event.LatestMessage,
			Location:      event.Location,
			LastEventAt:   event.LastEventAt,
		})
	}
	return s.store.CreatePackage(ctx, store.NewPackage{
		Description:        description,
		TrackingNumber:     trackingNumber,
		Carrier:            carrier,
		TrackingProvider:   providerName,
		TrackingProviderID: registration.ProviderID,
		Status:             status,
		SubStatus:          registration.SubStatus,
		LatestMessage:      registration.LatestMessage,
		LatestLocation:     registration.Location,
		LastEventAt:        registration.LastEventAt,
		TrackingError:      trackingError,
		Events:             events,
	})
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

func (s *Server) gmailStatus(w http.ResponseWriter, r *http.Request) {
	if s.gmail == nil {
		writeJSON(w, http.StatusOK, gmail.Status{Configured: false})
		return
	}
	status, err := s.gmail.Status(r.Context())
	if err != nil {
		s.internalError(w, "read Gmail status", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) startGmailOAuth(w http.ResponseWriter, _ *http.Request) {
	if s.gmail == nil {
		writeError(w, http.StatusServiceUnavailable, "Gmail is not configured on this server")
		return
	}
	authorizationURL, err := s.gmail.AuthURL()
	if err != nil {
		s.internalError(w, "start Gmail OAuth", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": authorizationURL})
}

func (s *Server) completeGmailOAuth(w http.ResponseWriter, r *http.Request) {
	if s.gmail == nil {
		writeError(w, http.StatusServiceUnavailable, "Gmail is not configured on this server")
		return
	}
	state := r.URL.Query().Get("state")
	if oauthError := r.URL.Query().Get("error"); oauthError != "" {
		_ = s.gmail.CompleteOAuth(r.Context(), state, "")
		http.Redirect(w, r, "/?gmail=denied", http.StatusSeeOther)
		return
	}
	if err := s.gmail.CompleteOAuth(r.Context(), state, r.URL.Query().Get("code")); err != nil {
		if errors.Is(err, gmail.ErrInvalidState) {
			writeError(w, http.StatusBadRequest, "invalid or expired Gmail authorization")
			return
		}
		s.logger.Warn("complete Gmail OAuth", "error", err)
		http.Redirect(w, r, "/?gmail=error", http.StatusSeeOther)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.gmail.Sync(ctx); err != nil {
			s.logger.Warn("initial Gmail sync failed", "error", err)
		}
	}()
	http.Redirect(w, r, "/?gmail=connected", http.StatusSeeOther)
}

func (s *Server) syncGmail(w http.ResponseWriter, r *http.Request) {
	if s.gmail == nil {
		writeError(w, http.StatusServiceUnavailable, "Gmail is not configured on this server")
		return
	}
	if err := s.gmail.Sync(r.Context()); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusConflict, "connect Gmail before scanning")
		return
	} else if err != nil {
		s.logger.Warn("sync Gmail", "error", err)
		writeError(w, http.StatusBadGateway, "could not scan Gmail")
		return
	}
	status, err := s.gmail.Status(r.Context())
	if err != nil {
		s.internalError(w, "read Gmail status", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) disconnectGmail(w http.ResponseWriter, r *http.Request) {
	if s.gmail == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.gmail.Disconnect(r.Context()); err != nil {
		s.logger.Warn("revoke Gmail access after local disconnect", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listEmailCandidates(w http.ResponseWriter, r *http.Request) {
	candidates, err := s.store.ListEmailCandidates(r.Context())
	if err != nil {
		s.internalError(w, "list email candidates", err)
		return
	}
	writeJSON(w, http.StatusOK, candidates)
}

func (s *Server) acceptEmailCandidate(w http.ResponseWriter, r *http.Request) {
	candidate, err := s.store.EmailCandidate(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "email suggestion not found")
		return
	}
	if err != nil {
		s.internalError(w, "read email candidate", err)
		return
	}
	if !trackingNumberPattern.MatchString(candidate.TrackingNumber) {
		writeError(w, http.StatusUnprocessableEntity, "email suggestion has an invalid tracking number")
		return
	}
	pkg, err := s.savePackage(r.Context(), candidate.Description, candidate.TrackingNumber, "")
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "that tracking number is already saved")
		return
	}
	if err != nil {
		s.internalError(w, "create package from email", err)
		return
	}
	if err := s.store.SetEmailCandidateStatus(r.Context(), candidate.ID, "accepted"); err != nil {
		s.internalError(w, "accept email candidate", err)
		return
	}
	writeJSON(w, http.StatusCreated, pkg)
}

func (s *Server) dismissEmailCandidate(w http.ResponseWriter, r *http.Request) {
	err := s.store.SetEmailCandidateStatus(r.Context(), r.PathValue("id"), "dismissed")
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "email suggestion not found")
		return
	}
	if err != nil {
		s.internalError(w, "dismiss email candidate", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) trackingWebhook(w http.ResponseWriter, r *http.Request) {
	if s.tracker == nil {
		writeError(w, http.StatusServiceUnavailable, "tracking provider is not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}
	update, err := s.tracker.ParseWebhook(r, body)
	switch {
	case errors.Is(err, tracking.ErrIgnoredWebhook):
		w.WriteHeader(http.StatusOK)
		return
	case errors.Is(err, tracking.ErrWebhookNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "tracking webhooks are not configured")
		return
	case errors.Is(err, tracking.ErrWebhookAuthentication):
		writeError(w, http.StatusUnauthorized, "invalid webhook authentication")
		return
	case errors.Is(err, tracking.ErrInvalidWebhook):
		writeError(w, http.StatusBadRequest, "invalid tracking webhook")
		return
	case err != nil:
		s.internalError(w, "parse tracking webhook", err)
		return
	}
	pkg, changed, err := s.store.UpdateTracking(r.Context(), update.TrackingNumber, update.Carrier, store.TrackingUpdate{
		Status:        update.Status,
		SubStatus:     update.SubStatus,
		LatestMessage: update.LatestMessage,
		Location:      update.Location,
		LastEventAt:   update.LastEventAt,
	})
	if errors.Is(err, sql.ErrNoRows) {
		// The provider account may contain trackers not owned by this instance.
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		s.internalError(w, "process tracking webhook", err)
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
	if pkg.LatestLocation != "" {
		body += " · " + pkg.LatestLocation
	}
	return title, body
}
