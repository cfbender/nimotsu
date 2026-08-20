package store

import (
	"context"
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
	updated, changed, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, 100003, TrackingUpdate{
		Status:        "InTransit",
		SubStatus:     "InTransit_Other",
		LatestMessage: "Departed facility",
		LastEventAt:   &eventAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || updated.Status != "InTransit" || updated.CarrierCode == nil || *updated.CarrierCode != 100003 {
		t.Fatalf("unexpected update: changed=%v package=%+v", changed, updated)
	}

	packages, err := dataStore.ListPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].LatestMessage != "Departed facility" {
		t.Fatalf("packages = %+v", packages)
	}
}
