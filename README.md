# Sonic Siphon

YouTube to MP3 with playback-speed control. Self-hosted, Docker-first, with a
machine-readable HTTP API designed to be driven by either the bundled web UI
or an automation/AI agent.

- Download single videos or full playlists at any speed (ffmpeg `atempo`)
- OpenAPI 3.1 spec at `/api/v1/openapi.json`, browsable docs at `/api/v1/docs`
- Optional static-admin auth via `.env` (login page, session cookies)
- Lightweight: a single Go binary plus `yt-dlp` and `ffmpeg`, no database

## Quick start

```bash
git clone https://github.com/Gren-95/sonic-siphon.git
cd sonic-siphon
docker compose up -d --build
open http://localhost:5000
```

Paste a YouTube URL, pick a speed, hit **Download**. Files land in
`./output` after you click *Move to done*. `/temp` holds in-progress and
unsorted files inside the container.

## Configuration (`.env`)

Copy `.env.example` to `.env` if you want auth or to flip docs visibility.
Everything is optional — the app runs without a `.env` file at all.

```env
# Both must be set for auth to turn on. Leave blank for an open instance.
ADMIN_USERNAME=admin
ADMIN_PASSWORD=changeme

# When auth is on, controls whether the OpenAPI spec/docs/schemas
# are reachable without logging in. Default true.
PUBLIC_DOCS=true
```

When auth is enabled:
- Browser GETs to a protected page redirect to `/login`
- API requests without a session return `401 {"error":"authentication required"}`
- `GET /api/v1/health` is always public so the docker healthcheck works
- Sessions are 24h `HttpOnly` cookies, evicted on a 5-minute janitor sweep
- Failed login attempts are rate-limited to 5 per 15 min per IP (`429` with
  `Retry-After`); successful logins do not consume quota

> Credentials are sent over plain HTTP, so this is only safe on localhost or
> behind a TLS-terminating reverse proxy. Don't expose the bare port to the
> internet.

## API

Browsable docs live at `/api/v1/docs`, OpenAPI spec at `/api/v1/openapi.json`.

## Tests

The suite is end-to-end: each test actually downloads "Never Gonna Give You
Up" via `yt-dlp` and re-encodes it with `ffmpeg`. There are no mocks. The
`sonic-siphon-test` container has both binaries baked in; start it once via
the `test` profile, then `docker exec` the scripts:

```bash
docker exec sonic-siphon-test ./scripts/test.sh
```

Each test calls a `requireBinaries` helper that `t.Skip`s when a tool is
missing, so the suite reports SKIP rather than FAIL on an environment
without yt-dlp.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
