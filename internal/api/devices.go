package api

import (
	"net/http"
	"strings"
)

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

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	if s.sendTestPush == nil {
		writeError(w, http.StatusServiceUnavailable, "Firebase push notifications are not configured on this server")
		return
	}
	tokens, err := s.store.ListDeviceTokens(r.Context())
	if err != nil {
		s.internalError(w, "list devices for test notification", err)
		return
	}
	if len(tokens) == 0 {
		writeError(w, http.StatusConflict, "enable notifications on a device before sending a test")
		return
	}
	sent, err := s.sendTestPush(r.Context(), tokens)
	if sent == 0 {
		s.logger.Error("send test notification", "error", err)
		writeError(w, http.StatusBadGateway, "Firebase did not accept the test notification")
		return
	}
	if err != nil {
		s.logger.Warn("some test notifications failed", "sent", sent, "failed", len(tokens)-sent, "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": sent})
}
