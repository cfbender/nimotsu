package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/cfbender/nimotsu/internal/store"
	"github.com/cfbender/nimotsu/internal/tracking"
)

var trackingNumberPattern = regexp.MustCompile(`^[A-Z0-9_-]{5,50}$`)

func (s *Server) listPackages(w http.ResponseWriter, r *http.Request) {
	packages, err := s.store.ListPackages(r.Context())
	if err != nil {
		s.internalError(w, "list packages", err)
		return
	}
	writeJSON(w, http.StatusOK, packages)
}

func (s *Server) refreshTracking(w http.ResponseWriter, r *http.Request) {
	if s.tracker == nil {
		writeError(w, http.StatusServiceUnavailable, "Shippo is not configured on this server")
		return
	}
	packages, err := s.store.ListPackages(r.Context())
	if err != nil {
		s.internalError(w, "list packages for tracking refresh", err)
		return
	}
	refreshed := 0
	failed := 0
	for _, pkg := range packages {
		if pkg.Status == "Delivered" {
			continue
		}
		registration, err := tracking.FetchRegistration(r.Context(), s.tracker, pkg)
		if err != nil {
			failed++
			s.logger.Warn("manual tracking refresh failed", "package_id", pkg.ID, "error", err)
			continue
		}
		updated, changed, updateErr := tracking.SaveRegistration(r.Context(), s.store, s.tracker.Name(), pkg, registration)
		if updateErr != nil {
			failed++
			s.logger.Error("save manual tracking refresh", "package_id", pkg.ID, "error", updateErr)
			continue
		}
		refreshed++
		if changed && updated.Allows(updated.Status) && s.onTrackingUpdate != nil {
			go s.onTrackingUpdate(updated)
		}
	}
	packages, err = s.store.ListPackages(r.Context())
	if err != nil {
		s.internalError(w, "list refreshed packages", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Packages  []store.Package `json:"packages"`
		Refreshed int             `json:"refreshed"`
		Failed    int             `json:"failed"`
	}{Packages: packages, Refreshed: refreshed, Failed: failed})
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
	return s.store.CreatePackage(ctx, store.NewPackage{
		Description:         description,
		TrackingNumber:      trackingNumber,
		Carrier:             carrier,
		TrackingProvider:    providerName,
		TrackingProviderID:  registration.ProviderID,
		Status:              status,
		SubStatus:           registration.SubStatus,
		LatestMessage:       registration.LatestMessage,
		LatestLocation:      registration.Location,
		EstimatedDeliveryAt: registration.EstimatedDeliveryAt,
		LastEventAt:         registration.LastEventAt,
		TrackingError:       trackingError,
		Events:              tracking.HistoryEvents(registration),
	})
}

func (s *Server) updatePackage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Description          *string                     `json:"description"`
		NotificationsEnabled *bool                       `json:"notifications_enabled"`
		NotificationSettings *store.NotificationSettings `json:"notification_settings"`
	}
	if err := readJSON(w, r, &request); err != nil {
		return
	}
	if request.Description == nil && request.NotificationsEnabled == nil && request.NotificationSettings == nil {
		writeError(w, http.StatusBadRequest, "description or notification settings are required")
		return
	}
	var pkg store.Package
	var err error
	if request.Description != nil {
		description := strings.TrimSpace(*request.Description)
		if description == "" {
			writeError(w, http.StatusBadRequest, "description is required")
			return
		}
		pkg, err = s.store.RenamePackage(r.Context(), r.PathValue("id"), description)
	}
	if err == nil && request.NotificationsEnabled != nil {
		pkg, err = s.store.SetNotifications(r.Context(), r.PathValue("id"), *request.NotificationsEnabled)
	}
	if err == nil && request.NotificationSettings != nil {
		pkg, err = s.store.SetPackageNotificationSettings(r.Context(), r.PathValue("id"), *request.NotificationSettings)
	}
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

func (s *Server) trackingWebhook(w http.ResponseWriter, r *http.Request) {
	if s.tracker == nil {
		writeError(w, http.StatusServiceUnavailable, "tracking provider is not configured")
		return
	}
	switch err := s.tracker.AuthenticateWebhook(r); {
	case errors.Is(err, tracking.ErrWebhookNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "tracking webhooks are not configured")
		return
	case errors.Is(err, tracking.ErrWebhookAuthentication):
		writeError(w, http.StatusUnauthorized, "invalid webhook authentication")
		return
	case err != nil:
		s.internalError(w, "authenticate tracking webhook", err)
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
	case errors.Is(err, tracking.ErrInvalidWebhook):
		writeError(w, http.StatusBadRequest, "invalid tracking webhook")
		return
	case err != nil:
		s.internalError(w, "parse tracking webhook", err)
		return
	}
	pkg, changed, err := s.store.UpdateTracking(r.Context(), update.TrackingNumber, update.Carrier, store.TrackingUpdate{
		Status:              update.Status,
		SubStatus:           update.SubStatus,
		LatestMessage:       update.LatestMessage,
		Location:            update.Location,
		EstimatedDeliveryAt: update.EstimatedDeliveryAt,
		LastEventAt:         update.LastEventAt,
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
	if changed && pkg.Allows(pkg.Status) && s.onTrackingUpdate != nil {
		go s.onTrackingUpdate(pkg)
	}
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
