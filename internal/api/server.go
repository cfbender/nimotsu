package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/cfbender/nimotsu/internal/gmail"
	"github.com/cfbender/nimotsu/internal/store"
	"github.com/cfbender/nimotsu/internal/tracking"
)

type Server struct {
	store            *store.Store
	tracker          tracking.Provider
	gmail            *gmail.Service
	apiToken         string
	onTrackingUpdate func(store.Package)
	sendTestPush     func(context.Context, []string) (int, error)
	logger           *slog.Logger
}

func New(dataStore *store.Store, trackerClient tracking.Provider, gmailService *gmail.Service, apiToken string, onTrackingUpdate func(store.Package), sendTestPush func(context.Context, []string) (int, error), logger *slog.Logger) http.Handler {
	server := &Server{
		store:            dataStore,
		tracker:          trackerClient,
		gmail:            gmailService,
		apiToken:         apiToken,
		onTrackingUpdate: onTrackingUpdate,
		sendTestPush:     sendTestPush,
		logger:           logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.Handle("GET /api/packages", server.authorize(http.HandlerFunc(server.listPackages)))
	mux.Handle("POST /api/packages", server.authorize(http.HandlerFunc(server.createPackage)))
	mux.Handle("POST /api/packages/refresh", server.authorize(http.HandlerFunc(server.refreshTracking)))
	mux.Handle("GET /api/packages/{id}/events", server.authorize(http.HandlerFunc(server.listTrackingEvents)))
	mux.Handle("PATCH /api/packages/{id}", server.authorize(http.HandlerFunc(server.updatePackage)))
	mux.Handle("DELETE /api/packages/{id}", server.authorize(http.HandlerFunc(server.archivePackage)))
	mux.Handle("GET /api/tracking/carrier", server.authorize(http.HandlerFunc(server.detectCarrier)))
	mux.Handle("POST /api/devices", server.authorize(http.HandlerFunc(server.registerDevice)))
	mux.Handle("POST /api/notifications/test", server.authorize(http.HandlerFunc(server.testNotification)))
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
