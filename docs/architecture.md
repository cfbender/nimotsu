# Architecture

## Decisions

### Go + SQLite in one container

The server uses Go's standard HTTP stack and SQLite in WAL mode. It serves the built React app itself in production, so a deployment needs one process, one persistent volume, and no Redis or separate database. The first version is intentionally a single-user instance protected by one bearer token.

### React + Capacitor, Android first

The same Vite/React codebase is the browser UI and Capacitor Android UI. The Android app stores the self-hosted server URL and bearer token locally, so one APK can connect to any instance. Capacitor is a good fit because the app is mostly a list and forms; native code is only needed for push registration.

Capacitor's Android push plugin uses Firebase Cloud Messaging. Building an APK does not bypass Android's notification permission: Android 13 and newer require a runtime grant. Nimotsu requests it only after an explicit user action. iOS/APNs is out of scope for the first release.

### Provider-neutral tracking with Shippo webhooks

The API depends on a small tracking-provider contract for registration and webhook parsing; package storage and notifications contain no Shippo-specific types. The current provider registers tracking with `POST /tracks/`. Shippo requires a carrier, so Nimotsu makes a conservative local guess for common formats and asks the user for a Shippo carrier token when the number is ambiguous.

Tracking changes arrive at `/api/webhooks/tracking`. Shippo's self-service webhook setup does not provide a signing secret, so the adapter authenticates a random token embedded in the configured webhook URL and converts `track_updated` events to provider-neutral updates. SQLite stores the carrier token and canonical package status. Repeated webhook deliveries are idempotent and do not send duplicate push notifications.

### Gmail OAuth with periodic sync

Gmail uses Google's web-server OAuth flow with `gmail.readonly`, offline access, a single-use CSRF state value, and AES-GCM encrypted token storage. For this self-hosted product, periodic Gmail API synchronization is preferable to Gmail push:

- Gmail push requires every administrator to provision a Pub/Sub topic, IAM grant, subscription, and public webhook.
- `users.watch` expires within seven days and must be renewed.
- a five-minute recent-mail poll keeps the deployment at one container and finds shipping messages even when Gmail rules archive them.

After linking, a worker queries likely shipping messages, extracts conservative tracking-number candidates, and puts them in a review queue. Message bodies are processed in memory but not persisted. It does not automatically register every number-like string because external Shippo tracking can be billable and emails contain many false positives.

The user flow remains one click after the instance administrator has supplied a Google OAuth client ID and secret. Self-hosting cannot eliminate that one-time Google Cloud configuration because each deployment needs an authorized redirect URI and a trusted OAuth client.

## Runtime flow

```diagram
┌───────────────┐  register   ┌──────────────┐
│ Go + SQLite   │────────────▶│  Shippo API  │
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

1. **Tracking slice (implemented):** CRUD, Shippo registration/webhooks behind a provider contract, Android shell, FCM.
2. **Gmail discovery (implemented):** OAuth, encrypted token storage, polling worker, candidate review.
3. **Notification durability:** SQLite outbox and dead-device cleanup; the current sender is best-effort after the webhook transaction commits.
4. **Hardening:** first-run token setup, rate limits, backup/restore, release signing, and update delivery.
