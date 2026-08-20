package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cfbender/nimotsu/internal/api"
	"github.com/cfbender/nimotsu/internal/config"
	"github.com/cfbender/nimotsu/internal/gmail"
	"github.com/cfbender/nimotsu/internal/push"
	"github.com/cfbender/nimotsu/internal/shippo"
	"github.com/cfbender/nimotsu/internal/store"
	"github.com/cfbender/nimotsu/internal/tracking"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configuration := config.Load()
	appContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dataStore, err := store.Open(configuration.DataPath)
	if err != nil {
		logger.Error("open data store", "error", err)
		os.Exit(1)
	}
	defer dataStore.Close()

	var trackingClient tracking.Provider
	if configuration.ShippoAPIToken != "" {
		trackingClient = shippo.New(configuration.ShippoAPIToken, configuration.ShippoWebhookToken)
		if configuration.ShippoWebhookToken == "" {
			logger.Warn("Shippo webhook authentication is not configured; real-time tracking updates are disabled")
		}
	} else if configuration.ShippoWebhookToken != "" {
		logger.Error("Shippo configuration is incomplete; the API token is required when a webhook token is set")
		os.Exit(1)
	} else {
		logger.Warn("Shippo is not configured; packages will be saved without registration")
	}
	if trackingClient != nil {
		go reconcileTracking(appContext, dataStore, trackingClient, logger)
	}

	var pushSender *push.Sender
	if configuration.FirebaseCredentials != "" {
		pushSender, err = push.NewSender(appContext, configuration.FirebaseCredentials)
		if err != nil {
			logger.Error("configure Firebase", "error", err)
			os.Exit(1)
		}
	} else {
		logger.Warn("Firebase is not configured; Android push notifications are disabled")
	}

	var gmailService *gmail.Service
	gmailValues := []string{configuration.GmailClientID, configuration.GmailClientSecret, configuration.GmailPublicURL, configuration.EncryptionKey}
	gmailValueCount := 0
	for _, value := range gmailValues {
		if value != "" {
			gmailValueCount++
		}
	}
	if gmailValueCount == len(gmailValues) {
		gmailService, err = gmail.New(gmail.Config{
			ClientID:      configuration.GmailClientID,
			ClientSecret:  configuration.GmailClientSecret,
			PublicURL:     configuration.GmailPublicURL,
			EncryptionKey: configuration.EncryptionKey,
		}, dataStore, logger)
		if err != nil {
			logger.Error("configure Gmail", "error", err)
			os.Exit(1)
		}
		go gmailService.Run(appContext, 5*time.Minute)
	} else if gmailValueCount > 0 {
		logger.Error("Gmail configuration is incomplete; client ID, client secret, public URL, and encryption key are all required")
		os.Exit(1)
	} else {
		logger.Warn("Gmail is not configured; email discovery is disabled")
	}

	sendPush := func(ctx context.Context, tokens []string, title, body, packageID string) (int, error) {
		sent := 0
		var sendErrors []error
		for _, token := range tokens {
			if err := pushSender.Send(ctx, token, title, body, packageID); err != nil {
				sendErrors = append(sendErrors, err)
				continue
			}
			sent++
		}
		return sent, errors.Join(sendErrors...)
	}
	onTrackingUpdate := func(pkg store.Package) {
		if pushSender == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		tokens, err := dataStore.ListDeviceTokens(ctx)
		if err != nil {
			logger.Error("list push devices", "error", err)
			return
		}
		title, body := api.NotificationText(pkg)
		if _, err := sendPush(ctx, tokens, title, body, pkg.ID); err != nil {
			logger.Error("send push notifications", "error", err)
		}
	}
	var sendTestPush func(context.Context, []string) (int, error)
	if pushSender != nil {
		sendTestPush = func(ctx context.Context, tokens []string) (int, error) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return sendPush(ctx, tokens, "Nimotsu test notification", "Push notifications are working.", "")
		}
	}

	apiHandler := api.New(dataStore, trackingClient, gmailService, configuration.APIToken, onTrackingUpdate, sendTestPush, logger)
	handler := withStaticFiles(apiHandler, configuration.WebDir)
	server := &http.Server{
		Addr:              configuration.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-appContext.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	logger.Info("nimotsu listening", "address", configuration.Listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "error", err)
		os.Exit(1)
	}
}

func reconcileTracking(ctx context.Context, dataStore *store.Store, provider tracking.Provider, logger *slog.Logger) {
	packages, err := dataStore.ListPackages(ctx)
	if err != nil {
		logger.Error("list packages for tracking reconciliation", "error", err)
		return
	}
	for _, pkg := range packages {
		if ctx.Err() != nil {
			return
		}
		registering := pkg.TrackingProvider != provider.Name()
		if trackingComplete(pkg.Status) && registering {
			continue
		}
		var registration tracking.Registration
		if registering {
			registration, err = provider.Register(ctx, pkg.TrackingNumber, pkg.Carrier)
		} else {
			registration, err = provider.Lookup(ctx, pkg.TrackingNumber, pkg.Carrier)
		}
		if err != nil {
			if registering {
				status := "RegistrationFailed"
				if errors.Is(err, tracking.ErrCarrierRequired) {
					status = "NeedsCarrier"
				}
				if updateErr := dataStore.SetRegistrationError(ctx, pkg.ID, pkg.Carrier, status, err.Error()); updateErr != nil {
					logger.Error("save tracking registration error", "package_id", pkg.ID, "error", updateErr)
				}
			}
			logger.Warn("tracking provider reconciliation failed", "package_id", pkg.ID, "error", err)
			continue
		}
		status := registration.Status
		if status == "" {
			status = "Registered"
		}
		carrier := pkg.Carrier
		if registration.Carrier != "" {
			carrier = registration.Carrier
		}
		updated, _, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, carrier, store.TrackingUpdate{
			Provider:            provider.Name(),
			ProviderID:          registration.ProviderID,
			Status:              status,
			SubStatus:           registration.SubStatus,
			LatestMessage:       registration.LatestMessage,
			Location:            registration.Location,
			EstimatedDeliveryAt: registration.EstimatedDeliveryAt,
			LastEventAt:         registration.LastEventAt,
		})
		if err != nil {
			logger.Error("save reconciled tracking registration", "package_id", pkg.ID, "error", err)
			continue
		}
		events := make([]store.TrackingUpdate, 0, len(registration.History)+1)
		for _, event := range registration.History {
			events = append(events, store.TrackingUpdate{
				Status: event.Status, SubStatus: event.SubStatus, LatestMessage: event.LatestMessage, Location: event.Location, LastEventAt: event.LastEventAt,
			})
		}
		events = append(events, store.TrackingUpdate{
			Status: registration.Status, SubStatus: registration.SubStatus, LatestMessage: registration.LatestMessage, Location: registration.Location, LastEventAt: registration.LastEventAt,
		})
		if err := dataStore.RecordTrackingEvents(ctx, updated.ID, events); err != nil {
			logger.Error("save reconciled tracking history", "package_id", pkg.ID, "error", err)
		}
	}
}

func trackingComplete(status string) bool {
	switch status {
	case "Delivered", "Expired", "Cancelled", "Canceled":
		return true
	default:
		return false
	}
}

func withStaticFiles(apiHandler http.Handler, webDir string) http.Handler {
	if webDir == "" {
		return apiHandler
	}
	files := http.FileServer(http.Dir(webDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		path := filepath.Join(webDir, strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/"))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(webDir, "index.html"))
	})
}
