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
	"github.com/cfbender/nimotsu/internal/push"
	"github.com/cfbender/nimotsu/internal/seventeentrack"
	"github.com/cfbender/nimotsu/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	configuration := config.Load()

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
		pushSender, err = push.NewSender(context.Background(), configuration.FirebaseCredentials)
		if err != nil {
			logger.Error("configure Firebase", "error", err)
			os.Exit(1)
		}
	} else {
		logger.Warn("Firebase is not configured; Android push notifications are disabled")
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

	apiHandler := api.New(dataStore, trackingClient, configuration.APIToken, configuration.SeventeenTrackKey, onTrackingUpdate, logger)
	handler := withStaticFiles(apiHandler, configuration.WebDir)
	server := &http.Server{
		Addr:              configuration.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
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
