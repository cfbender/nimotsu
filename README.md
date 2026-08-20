# Nimotsu

Nimotsu is a small, self-hosted package tracker with an Android app. A single Go process serves the API and web app, stores data in SQLite, registers shipments with 17TRACK, receives signed tracking webhooks, and sends status updates through Firebase Cloud Messaging.

The repository currently contains the first end-to-end slice:

- mobile-first package list, manual add, carrier auto-detection, archive, and per-package notification toggle
- 17TRACK v2.4 registration and signed `TRACKING_UPDATED` webhook handling
- Capacitor 8 Android shell and device push-token registration
- FCM HTTP v1 delivery from the Go server
- Gmail OAuth, encrypted token storage, periodic inbox scanning, and a review queue
- one-container deployment foundation

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
| `NIMOTSU_GMAIL_CLIENT_ID` | Google OAuth web client ID; optional, but required with the other Gmail settings. |
| `NIMOTSU_GMAIL_CLIENT_SECRET` | Google OAuth web client secret. |
| `NIMOTSU_PUBLIC_URL` | Public HTTPS origin of the instance, such as `https://packages.example.com`. |
| `NIMOTSU_ENCRYPTION_KEY` | Base64-encoded 32-byte key used to encrypt Gmail OAuth tokens. |
| `NIMOTSU_DATA_PATH` | SQLite path; defaults to `./data/nimotsu.db`. |
| `NIMOTSU_WEB_DIR` | Built web assets; defaults to `./web/dist`. |

Configure this URL in the 17TRACK dashboard:

```text
https://your-nimotsu-host.example/api/webhooks/17track
```

17TRACK signs this endpoint with the same API key. The server rejects unsigned or modified payloads.

### Gmail integration

Each self-hosted instance uses its administrator's Google OAuth project:

1. In Google Cloud, enable the **Gmail API**, configure the OAuth consent screen, and add intended accounts as test users while the app is in testing mode.
2. Create an OAuth 2.0 client with application type **Web application**.
3. Add this exact authorized redirect URI, replacing the origin with `NIMOTSU_PUBLIC_URL`:

   ```text
   https://packages.example.com/api/gmail/oauth/callback
   ```

4. Set the client ID, client secret, public URL, and a persistent encryption key in `.env`:

   ```sh
   openssl rand -base64 32
   ```

5. Restart Nimotsu, open **Settings → Gmail**, and choose **Connect Gmail**.

Nimotsu requests only `gmail.readonly`. It scans up to 50 likely shipping messages every five minutes, stores encrypted OAuth tokens and extracted review candidates in SQLite, and does not store message bodies. A package is created only after the user accepts a candidate.

Google classifies `gmail.readonly` as a restricted scope. A publicly distributed deployment may need Google app verification and a security assessment. The intended self-hosted setup is for each administrator to own the OAuth project and explicitly authorize its users.

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
