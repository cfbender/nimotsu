package tracking

import (
	"context"
	"errors"
	"log/slog"

	"github.com/cfbender/nimotsu/internal/store"
)

// FetchRegistration registers the package with the provider when the provider
// does not own it yet, and otherwise looks up its current tracking state.
func FetchRegistration(ctx context.Context, provider Provider, pkg store.Package) (Registration, error) {
	if pkg.TrackingProvider == provider.Name() {
		return provider.Lookup(ctx, pkg.TrackingNumber, pkg.Carrier)
	}
	return provider.Register(ctx, pkg.TrackingNumber, pkg.Carrier)
}

// SaveRegistration persists a registration or lookup result: it updates the
// package's tracking state and records the provider's event history. Provider
// ownership is stamped only when the package is not already owned, so webhook
// stale-event protection stays active for owned packages.
func SaveRegistration(ctx context.Context, dataStore *store.Store, providerName string, pkg store.Package, registration Registration) (store.Package, bool, error) {
	status := registration.Status
	if status == "" {
		status = "Registered"
	}
	carrier := pkg.Carrier
	if registration.Carrier != "" {
		carrier = registration.Carrier
	}
	provider := ""
	providerID := ""
	if pkg.TrackingProvider != providerName || pkg.TrackingProviderID == "" {
		provider = providerName
		providerID = registration.ProviderID
	}
	updated, changed, err := dataStore.UpdateTracking(ctx, pkg.TrackingNumber, carrier, store.TrackingUpdate{
		Provider:            provider,
		ProviderID:          providerID,
		Status:              status,
		SubStatus:           registration.SubStatus,
		LatestMessage:       registration.LatestMessage,
		Location:            registration.Location,
		EstimatedDeliveryAt: registration.EstimatedDeliveryAt,
		LastEventAt:         registration.LastEventAt,
	})
	if err != nil {
		return store.Package{}, false, err
	}
	events := append(HistoryEvents(registration), store.TrackingUpdate{
		Status:        status,
		SubStatus:     registration.SubStatus,
		LatestMessage: registration.LatestMessage,
		Location:      registration.Location,
		LastEventAt:   registration.LastEventAt,
	})
	if err := dataStore.RecordTrackingEvents(ctx, updated.ID, events); err != nil {
		return store.Package{}, false, err
	}
	return updated, changed, nil
}

// HistoryEvents converts a registration's provider history into storable
// tracking events.
func HistoryEvents(registration Registration) []store.TrackingUpdate {
	events := make([]store.TrackingUpdate, 0, len(registration.History)+1)
	for _, event := range registration.History {
		events = append(events, store.TrackingUpdate{
			Status:        event.Status,
			SubStatus:     event.SubStatus,
			LatestMessage: event.LatestMessage,
			Location:      event.Location,
			LastEventAt:   event.LastEventAt,
		})
	}
	return events
}

// Reconcile runs once at startup: it registers unowned, non-terminal packages
// with the provider and refreshes the packages the provider already owns, so
// upgrades and restarts never create duplicate provider registrations.
func Reconcile(ctx context.Context, dataStore *store.Store, provider Provider, logger *slog.Logger) {
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
		if registering && terminalStatus(pkg.Status) {
			continue
		}
		registration, err := FetchRegistration(ctx, provider, pkg)
		if err != nil {
			if registering {
				status := "RegistrationFailed"
				if errors.Is(err, ErrCarrierRequired) {
					status = "NeedsCarrier"
				}
				if updateErr := dataStore.SetRegistrationError(ctx, pkg.ID, pkg.Carrier, status, err.Error()); updateErr != nil {
					logger.Error("save tracking registration error", "package_id", pkg.ID, "error", updateErr)
				}
			}
			logger.Warn("tracking provider reconciliation failed", "package_id", pkg.ID, "error", err)
			continue
		}
		if _, _, err := SaveRegistration(ctx, dataStore, provider.Name(), pkg, registration); err != nil {
			logger.Error("save reconciled tracking registration", "package_id", pkg.ID, "error", err)
		}
	}
}

func terminalStatus(status string) bool {
	switch status {
	case "Delivered", "Expired", "Cancelled", "Canceled":
		return true
	default:
		return false
	}
}
