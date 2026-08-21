package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/cfbender/nimotsu/internal/gmail"
	"github.com/cfbender/nimotsu/internal/store"
)

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
