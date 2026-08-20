package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPackageLifecycle(t *testing.T) {
	dataStore, err := Open(filepath.Join(t.TempDir(), "nimotsu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()

	ctx := context.Background()
	pkg, err := dataStore.CreatePackage(ctx, NewPackage{
		Description:    "Headphones",
		TrackingNumber: "1Z999AA10123456784",
		Status:         "Registered",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreatePackage(ctx, NewPackage{Description: "Duplicate", TrackingNumber: pkg.TrackingNumber, Status: "Registered"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate error = %v, want ErrDuplicate", err)
	}

	eventAt := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	estimatedDeliveryAt := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	updated, changed, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, "UPS", TrackingUpdate{
		Status:              "InTransit",
		SubStatus:           "InTransit_Other",
		LatestMessage:       "Departed facility",
		EstimatedDeliveryAt: &estimatedDeliveryAt,
		LastEventAt:         &eventAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || updated.Status != "InTransit" || updated.Carrier != "UPS" || updated.EstimatedDeliveryAt == nil || !updated.EstimatedDeliveryAt.Equal(estimatedDeliveryAt) {
		t.Fatalf("unexpected update: changed=%v package=%+v", changed, updated)
	}
	duplicate, changed, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, "UPS", TrackingUpdate{
		Status:              "InTransit",
		SubStatus:           "InTransit_Other",
		LatestMessage:       "Departed facility",
		EstimatedDeliveryAt: &estimatedDeliveryAt,
		LastEventAt:         &eventAt,
	})
	if err != nil || changed || duplicate.Status != "InTransit" || duplicate.EstimatedDeliveryAt == nil {
		t.Fatalf("duplicate update: changed=%v package=%+v error=%v", changed, duplicate, err)
	}
	staleAt := eventAt.Add(-time.Hour)
	stale, changed, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, "UPS", TrackingUpdate{
		Status: "Delivered", LastEventAt: &staleAt,
	})
	if err != nil || changed || stale.Status != "InTransit" || !stale.LastEventAt.Equal(eventAt) {
		t.Fatalf("stale update: changed=%v package=%+v error=%v", changed, stale, err)
	}
	newerAt := eventAt.Add(time.Hour)
	newer, changed, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, "UPS", TrackingUpdate{
		Status:              "InTransit",
		SubStatus:           "InTransit_Other",
		LatestMessage:       "Departed facility",
		EstimatedDeliveryAt: &estimatedDeliveryAt,
		LastEventAt:         &newerAt,
	})
	if err != nil || !changed || !newer.LastEventAt.Equal(newerAt) {
		t.Fatalf("newer update: changed=%v package=%+v error=%v", changed, newer, err)
	}

	packages, err := dataStore.ListPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].LatestMessage != "Departed facility" || packages[0].EstimatedDeliveryAt == nil || !packages[0].EstimatedDeliveryAt.Equal(estimatedDeliveryAt) {
		t.Fatalf("packages = %+v", packages)
	}
	renamed, err := dataStore.RenamePackage(ctx, pkg.ID, "Studio headphones")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Description != "Studio headphones" {
		t.Fatalf("renamed package = %+v", renamed)
	}

	if err := dataStore.ArchivePackage(ctx, pkg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.RenamePackage(ctx, pkg.ID, "Archived headphones"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archived rename error = %v, want no rows", err)
	}
	if _, _, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, "UPS", TrackingUpdate{Status: "Delivered"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archived tracking update error = %v, want no rows", err)
	}
	restored, err := dataStore.CreatePackage(ctx, NewPackage{
		Description:    "Headphones again",
		TrackingNumber: pkg.TrackingNumber,
		Status:         "Registered",
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != pkg.ID || restored.Archived || restored.Description != "Headphones again" || restored.LatestMessage != "" {
		t.Fatalf("restored package = %+v", restored)
	}
	packages, err = dataStore.ListPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].ID != pkg.ID {
		t.Fatalf("packages after restore = %+v", packages)
	}
}

func TestTrackingEventsPersistRegistrationHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nimotsu.db")
	dataStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	currentAt := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	labelAt := currentAt.Add(-24 * time.Hour)
	pkg, err := dataStore.CreatePackage(t.Context(), NewPackage{
		Description:        "Headphones",
		TrackingNumber:     "1Z999AA10123456784",
		Carrier:            "ups",
		TrackingProvider:   "shippo",
		TrackingProviderID: "track-1",
		Status:             "InTransit",
		LatestMessage:      "Departed facility",
		LatestLocation:     "Los Angeles, CA 90001, US",
		LastEventAt:        &currentAt,
		Events: []TrackingUpdate{{
			Status: "PreTransit", LatestMessage: "Label created", Location: "Phoenix, AZ 85001, US", LastEventAt: &labelAt,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.LatestLocation != "Los Angeles, CA 90001, US" {
		t.Fatalf("latest location = %q", pkg.LatestLocation)
	}
	assertEvents := func(want int) {
		t.Helper()
		events, err := dataStore.ListTrackingEvents(t.Context(), pkg.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != want || events[0].Status != "InTransit" || events[0].Location != "Los Angeles, CA 90001, US" || events[1].Status != "PreTransit" || events[1].Location != "Phoenix, AZ 85001, US" || !events[1].OccurredAt.Equal(labelAt) {
			t.Fatalf("events = %+v", events)
		}
	}
	assertEvents(2)
	if err := dataStore.Close(); err != nil {
		t.Fatal(err)
	}

	dataStore, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	assertEvents(2)
	if err := dataStore.ArchivePackage(t.Context(), pkg.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ListTrackingEvents(t.Context(), pkg.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archived package history error = %v, want sql.ErrNoRows", err)
	}
}

func TestOpenMigratesExistingPackageCarrier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nimotsu.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
CREATE TABLE packages (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    tracking_number TEXT NOT NULL UNIQUE,
    carrier_code INTEGER,
    status TEXT NOT NULL,
    sub_status TEXT NOT NULL DEFAULT '',
    latest_message TEXT NOT NULL DEFAULT '',
    last_event_at INTEGER,
    tracking_error TEXT NOT NULL DEFAULT '',
    notifications_enabled INTEGER NOT NULL DEFAULT 1,
    archived INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
INSERT INTO packages (
    id, description, tracking_number, carrier_code, status, created_at, updated_at
) VALUES ('package-1', 'Headphones', '9400110898825022579493', 21051, 'RegistrationFailed', 1, 1);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	dataStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	packages, err := dataStore.ListPackages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Carrier != "" || packages[0].TrackingNumber != "9400110898825022579493" {
		t.Fatalf("migrated packages = %+v", packages)
	}
}
