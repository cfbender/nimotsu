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
	"github.com/cfbender/nimotsu/internal/seventeentrack"
	"github.com/cfbender/nimotsu/internal/store"
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

	var trackingClient api.Tracker
	if configuration.SeventeenTrackKey != "" {
		trackingClient = seventeentrack.New(configuration.SeventeenTrackKey)
	} else {
		logger.Warn("17TRACK is not configured; packages will be saved without registration")
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
		for _, token := range tokens {
			if err := pushSender.Send(ctx, token, title, body, pkg.ID); err != nil {
				logger.Error("send push notification", "error", err)
			}
		}
	}

	apiHandler := api.New(dataStore, trackingClient, gmailService, configuration.APIToken, configuration.SeventeenTrackKey, onTrackingUpdate, logger)
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
