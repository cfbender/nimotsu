# Nimotsu

Nimotsu is a small, self-hosted package tracker with an Android app. A single Go process serves the API and web app, stores data in SQLite, registers shipments with 17TRACK, receives signed tracking webhooks, and sends status updates through Firebase Cloud Messaging.

The repository currently contains the first end-to-end slice:

- mobile-first package list, manual add, carrier auto-detection, archive, and per-package notification toggle
- 17TRACK v2.4 registration and signed `TRACKING_UPDATED` webhook handling
- Capacitor 8 Android shell and device push-token registration
- FCM HTTP v1 delivery from the Go server
- one-container deployment foundation

Gmail linking and tracking-number suggestions are designed but not implemented yet. See [Architecture](docs/architecture.md).

## Development

Toolchains are pinned with [mise](https://mise.jdx.dev/) and JavaScript dependencies use [aube](https://aube.jdx.dev/).

```sh
./.agents/setup
mise run check
scripts/dev
```

The setup script installs Go, Node, Java, aube, and the Android 36 command-line SDK. It also activates mise in both login and interactive Bash shells so orb services see the same toolchain.

### Amp orbs

```sh
amp orb services ensure
```

This starts the Go API and Vite frontend as one supervised portal service. The Vite server proxies `/api` to Go, including the 17TRACK webhook route.

## Configuration

Copy `.env.example` to `.env`. The development server inherits exported variables; Docker Compose reads `.env` directly.

| Variable | Purpose |
| --- | --- |
| `NIMOTSU_API_TOKEN` | Bearer token used by the web and Android clients. Strongly recommended outside a trusted network. |
| `NIMOTSU_17TRACK_KEY` | 17TRACK API security key used for registration and webhook signature verification. |
| `NIMOTSU_FIREBASE_CREDENTIALS` | Path to a Firebase service-account JSON file with FCM send access. |
| `NIMOTSU_DATA_PATH` | SQLite path; defaults to `./data/nimotsu.db`. |
| `NIMOTSU_WEB_DIR` | Built web assets; defaults to `./web/dist`. |

Configure this URL in the 17TRACK dashboard:

```text
https://your-nimotsu-host.example/api/webhooks/17track
```

17TRACK signs this endpoint with the same API key. The server rejects unsigned or modified payloads.

## Android APK

1. Create a Firebase Android app with package ID `dev.nimotsu.app`.
2. Put its `google-services.json` at `android/app/google-services.json` (the file is gitignored).
3. Set the backend's `NIMOTSU_FIREBASE_CREDENTIALS` to the matching service-account JSON path.
4. Build the debug APK:

   ```sh
   mise exec -- aube run android:apk
   ```

The APK is written to `android/app/build/outputs/apk/debug/app-debug.apk`. On first launch, enter the HTTPS URL of your Nimotsu server and its API token. Android 13+ requires the user to grant notification permission; the app asks only when **Enable notifications** is tapped.

## Container

```sh
cp .env.example .env
docker compose up --build
```

The compose service stores SQLite under the `nimotsu-data` volume and listens on port 8080. Mount the Firebase service-account JSON into the container if push is enabled.

## Checks

```sh
mise run check       # Go tests, TypeScript, and Vitest
mise run build       # checks plus server, web, and debug APK builds
```
