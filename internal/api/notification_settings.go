package api

import (
	"net/http"

	"github.com/cfbender/nimotsu/internal/store"
)

func (s *Server) getNotificationDefaults(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.NotificationDefaults(r.Context())
	if err != nil {
		s.internalError(w, "read notification defaults", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateNotificationDefaults(w http.ResponseWriter, r *http.Request) {
	var settings store.NotificationSettings
	if err := readJSON(w, r, &settings); err != nil {
		return
	}
	updated, err := s.store.SetNotificationDefaults(r.Context(), settings)
	if err != nil {
		s.internalError(w, "update notification defaults", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
