# Architecture

## Decisions

### Go + SQLite in one container

The server uses Go's standard HTTP stack and SQLite in WAL mode. It serves the built React app itself in production, so a deployment needs one process, one persistent volume, and no Redis or separate database. The first version is intentionally a single-user instance protected by one bearer token.

### React + Capacitor, Android first

The same Vite/React codebase is the browser UI and Capacitor Android UI. The Android app stores the self-hosted server URL and bearer token locally, so one APK can connect to any instance. Capacitor is a good fit because the app is mostly a list and forms; native code is only needed for push registration.

Capacitor's Android push plugin uses Firebase Cloud Messaging. Building an APK does not bypass Android's notification permission: Android 13 and newer require a runtime grant. Nimotsu requests it only after an explicit user action. iOS/APNs is out of scope for the first release.

### 17TRACK webhooks, not polling

Creating a package calls `POST /track/v2.4/register` and omits `carrier` by default, allowing 17TRACK to return its best carrier guess. Tracking changes arrive at `/api/webhooks/17track`. The handler verifies the documented SHA-256 signature over the exact request body and API key before updating SQLite.

17TRACK owns the carrier/status normalization. Nimotsu stores the provider's numeric carrier code and canonical status rather than maintaining a second carrier rules engine.

### Gmail OAuth with periodic sync

Gmail should use Google's web-server OAuth flow with `gmail.readonly`, offline access, a CSRF state value, and encrypted refresh-token storage. For this self-hosted product, periodic Gmail API synchronization is preferable to Gmail push:

- Gmail push requires every administrator to provision a Pub/Sub topic, IAM grant, subscription, and public webhook.
- `users.watch` expires within seven days and must be renewed.
- a five-minute inbox poll keeps the deployment at one container and is sufficient for shipment discovery.

After linking, a worker will query messages newer than the last successful watermark, extract conservative carrier-specific candidates, and put them in a review queue. It must not automatically register every number-like string because 17TRACK registration consumes quota and emails contain many false positives.

The user flow remains one click after the instance administrator has supplied a Google OAuth client ID and secret. Self-hosting cannot eliminate that one-time Google Cloud configuration because each deployment needs an authorized redirect URI and a trusted OAuth client.

## Runtime flow

```diagram
┌───────────────┐  register   ┌──────────────┐
│ Go + SQLite   │────────────▶│ 17TRACK API  │
│               │◀────────────│              │
│               │  webhook    └──────────────┘
│               │
│               │  FCM v1     ┌──────────────┐     ┌─────────────┐
│               │────────────▶│ Firebase FCM │────▶│ Android APK │
└───────▲───────┘              └──────────────┘     └──────┬──────┘
        │ REST                                             │
        └──────────────────────────────────────────────────┘
```

## Delivery order

1. **Tracking slice (implemented):** CRUD, 17TRACK registration/webhooks, Android shell, FCM.
2. **Gmail discovery:** OAuth, encrypted token storage, polling worker, candidate review.
3. **Notification durability:** SQLite outbox and dead-device cleanup; the current sender is best-effort after the webhook transaction commits.
4. **Hardening:** first-run token setup, rate limits, backup/restore, release signing, and update delivery.
