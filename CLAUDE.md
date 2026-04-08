# The Box — Agent Handover

## What This Is

A recreation of the 1990s interactive music video TV channel "The Box". Users call a phone number (via Jambonz IVR) or use a web page to enter a 3-digit catalogue code. The requested music video gets queued and plays on a full-screen channel output, with a ticker bar cycling through the catalogue and upcoming requests — like teletext meets MTV.

## Canonical Reference

The original system is documented in **US Patent 6,124,854** (Sartain et al., assigned to The Box Worldwide LLC, filed 1997, granted 2000). When designing new features, prefer behaviours described in that patent over inventing new ones — it's the most accurate single source for "what The Box actually did". Some patent details that have shaped this codebase:

- **Selection feedback (FIG. 1, step 32 — "DISPLAY SELECTION #")**: when a request is accepted, the channel must give visible on-screen confirmation of the catalogue number. This is the *quick feedback to the selecting subscriber* the patent calls out explicitly.
- **Scroll bar content rules (col. 3, lines 36–44)**: the ticker is allowed to carry exactly five content types — (1) info about the currently playing video, (2) advertisements, (3) trivial/factual information *preferably related to music*, (4) news, (5) info about other available videos. Stay within those categories.
- **Logo overlay (col. 14)**: a logo bug overlaid on video programs is patent-sanctioned. We composite `BoxLogo.jsx` over `VideoPlayer.jsx`.
- **Stop sets / commercial breaks (col. 15–16)**: *"commercial breaks are assembled and inserted into the video programming on-the-fly… stop sets having a duration of any desired length."* See the **Ad Breaks** section below.
- **Vote stacking (col. 7, lines 14–22)**: *"a video which is already in the queue and then is selected by another subscriber, is moved forward."* See `internal/queue/queue.go`.
- **Empty-queue filler (col. 14)**: random play of a cached video is the patent's "queue was empty" fallback, not a separate feature.

## Architecture

**Go backend (`boxd`)** — single binary. Embeds the React frontend at compile time via `//go:embed`. Serves API, WebSocket, media files, IVR webhooks, and the channel SPA. SQLite with WAL mode, single writer.

**React frontend (`web/channel/`)** — Vite + React 18. Connects to backend via WebSocket at `/ws`. The playout controller on the backend drives all state — the frontend is a dumb renderer. No client-side routing.

**Two binaries:** `boxd` (server) and `boxctl` (CLI for catalogue management).

## Build

Frontend must be built before backend (assets are embedded):
```
make build   # or: cd web/channel && npm run build && cd ../.. && go build -o boxd ./cmd/boxd
```

**Important:** Vite outputs to `cmd/boxd/static/` with `assetsDir: 'static'` (not the default `assets/`) to avoid a route collision with `/assets/` which serves jingles from disk.

## Key Design Decisions

- **3-digit catalogue codes** (001-999), not 4-digit. Codes auto-increment.
- **Videos start muted then unmute** to satisfy browser autoplay policy. For headless capture pipelines, use `--autoplay-policy=no-user-gesture-required`.
- **Stale request cleanup on startup** — `playing`/`fetching` requests get reset to `queued` when `boxd` starts, so unclean shutdowns don't leave the queue stuck.
- **yt-dlp path** defaults to `"yt-dlp"` (PATH lookup), not a hardcoded absolute path. Override in `configs/config.yaml` if needed.
- **Prefetch worker** runs every 5s, fetches and transcodes the next N videos in the queue ahead of time.

## Project Layout

```
cmd/boxd/main.go          — HTTP server, API handlers, embedded SPA
cmd/boxctl/main.go         — CLI tool for catalogue management
internal/
  catalogue/catalogue.go   — CRUD for video catalogue (SQLite)
  config/config.go         — YAML config with sensible defaults
  db/db.go                 — SQLite setup, auto-migration
  fetcher/fetcher.go       — yt-dlp download, FFmpeg transcode, cache eviction
  ivr/handlers.go          — Jambonz webhook handlers (call, DTMF, status)
  playout/playout.go       — State machine: filler → transition → playing → filler
  queue/queue.go           — Request queue with rate limiting and dedup
  ws/hub.go                — WebSocket hub with last-state replay for new clients
web/channel/               — React frontend (Vite)
configs/config.yaml        — Runtime config
```

## Playout State Machine

`filler` → `transition` → `playing` → (optional `ad_break`) → back to `filler` (or next in queue)

- **Filler** cycles between ident screen and catalogue scroll. After `filler_random_delay_minutes`, plays a random cached video.
- **Transition** shows "coming up" overlay for `transition_seconds`.
- **Playing** streams video. Safety timer advances queue if renderer doesn't report `video_ended`.
- **Ad break** plays a random sting from `ads_dir` between requested videos. See below.

## Ad Breaks (Stop Sets)

Patent-faithful "stop sets" between requested videos. Drop short `.mp4`/`.webm`/`.mov` station-ID stings into `assets/ads/` and one will play between every N requested videos (configured via `playout.ads_every_n_videos` in `configs/config.yaml`; set to `0` to disable).

- Served at `/ads/<filename>` by `cmd/boxd/main.go`.
- Picked at random in `internal/playout/playout.go` `tryPlayAdBreak()`.
- Triggered from `advanceQueue()` only when there is a next queued video to come back to (no ad break before idling into filler).
- Ads broadcast a `play` WebSocket message with `video.is_ad: true`. The frontend hides `NowPlaying`, the code badge, and `BottomTicker` while `is_ad` is true; the `BoxLogo` bug stays visible.
- Safety timer = `ad_max_seconds` (default 90s) in case `video_ended` doesn't fire.

## Common Tasks

**Add a video:** `./boxctl add --youtube <YOUTUBE_ID>` or `POST /api/catalogue` with `{"youtube_id": "..."}`

**Request a video:** `POST /api/queue` with `{"code": "001"}`

**Pre-cache a video:** `POST /api/catalogue/001/cache`

**Skip current:** `POST /api/queue/skip`

## Gotchas

- The frontend embed means you must rebuild both frontend AND backend after any React/CSS change: `cd web/channel && npm run build && cd ../.. && go build -o boxd ./cmd/boxd`
- Browser cache can be aggressive — hard refresh (`Cmd+Shift+R`) after rebuilds.
- SQLite single-writer: `db.SetMaxOpenConns(1)`. Don't try to parallelise writes.
- The `handleRequestPage` in `cmd/boxd/main.go` is inline HTML via `fmt.Fprintf`, not a template. Phone number is injected from config.
