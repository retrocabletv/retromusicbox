# retromusicbox — Agent Handover

## What This Is

A recreation of the 1990s interactive music video TV channel format. Users enter a 3-digit catalogue code — via a web page, a phone line (any IVR provider), or a DIY DTMF rig — and the requested music video gets queued and plays on a full-screen channel output, with a ticker bar cycling through the catalogue and upcoming requests, like teletext meets MTV.

> **Naming note.** "The Box" almost certainly still reads on a live trademark lineage (Viacom / Paramount). This project uses the neutral name **retromusicbox** and binary names `rmbd` / `rmbctl`. The `ChannelLogo` component renders whatever image is at `/box-logo.png` — swap it out for your own artwork before broadcasting anywhere public.

## Canonical Reference

The original system is documented in **US Patent 6,124,854** (Sartain et al., filed 1997, granted 2000). When designing new features, prefer behaviours described in that patent over inventing new ones — it's the most accurate single source for "what the format actually did". Relevant details shaping this codebase:

- **Selection feedback (FIG. 1, step 32 — "DISPLAY SELECTION #")**: when a request is accepted, the channel must give visible on-screen confirmation of the catalogue number. The `RequestDigits` overlay reacts to `dial_update` WebSocket events from the IVR session handler.
- **Scroll bar content rules (col. 3, lines 36–44)**: the ticker may carry exactly five content types — (1) info about the currently playing video, (2) advertisements, (3) trivial/factual information *preferably related to music*, (4) news, (5) info about other available videos. Stay within those categories.
- **Logo overlay (col. 14)**: a logo bug overlaid on video programs is patent-sanctioned. We composite `ChannelLogo.jsx` over `VideoPlayer.jsx`.
- **Stop sets / commercial breaks (col. 15–16)**: *"commercial breaks are assembled and inserted into the video programming on-the-fly… stop sets having a duration of any desired length."* See the **Ad Breaks** section below.
- **Vote stacking (col. 7, lines 14–22)**: *"a video which is already in the queue and then is selected by another subscriber, is moved forward."* See `internal/queue/queue.go`.
- **Empty-queue filler (col. 14)**: random play of a cached video is the patent's "queue was empty" fallback, not a separate feature.
- **Multiple simultaneous callers (col. 6, FIG. 2)**: the channel displays several callers entering digits at once. The IVR session API caps concurrent sessions at `ivr.MaxConcurrent` (default 3).

## Architecture

**Go backend (`rmbd`)** — single binary. Embeds the React frontend at compile time via `//go:embed`. Serves API, WebSocket, media files, the IVR session API, and the channel SPA. SQLite with WAL mode, single writer.

**React frontend (`web/channel/`)** — Vite + React 18. Connects to backend via WebSocket at `/ws`. The playout controller on the backend drives all state — the frontend is a dumb renderer. No client-side routing.

**Two binaries:** `rmbd` (server) and `rmbctl` (CLI for catalogue management).

## Build

Frontend must be built before backend (assets are embedded):

```
make build   # or: cd web/channel && npm run build && cd ../.. && go build -o rmbd ./cmd/rmbd
```

**Important:** Vite outputs to `cmd/rmbd/static/` with `assetsDir: 'static'` (not the default `assets/`) to avoid a route collision with `/assets/` which serves jingles from disk.

## Key Design Decisions

- **3-digit catalogue codes** (001-999), not 4-digit. Codes auto-increment.
- **Videos start muted then unmute** to satisfy browser autoplay policy. For headless capture pipelines, use `--autoplay-policy=no-user-gesture-required`.
- **Stale request cleanup on startup** — `playing`/`fetching` requests get reset to `queued` when `rmbd` starts, so unclean shutdowns don't leave the queue stuck.
- **yt-dlp path** defaults to `"yt-dlp"` (PATH lookup), not a hardcoded absolute path. Override in `configs/config.yaml` if needed.
- **Prefetch worker** runs every 5s, fetches and transcodes the next N videos in the queue ahead of time.
- **IVR is service-agnostic.** The backend exposes a small REST session API; any DTMF/voice front-end (Jambonz, Twilio, Asterisk, a Pi rigged to a landline) can drive it. We do not bake in any particular provider's webhook shape.

## Project Layout

```
cmd/rmbd/main.go          — HTTP server, API handlers, embedded SPA
cmd/rmbctl/main.go        — CLI tool for catalogue management
internal/
  catalogue/catalogue.go  — CRUD for video catalogue (SQLite)
  config/config.go        — YAML config with sensible defaults
  db/db.go                — SQLite setup, auto-migration
  fetcher/fetcher.go      — yt-dlp download, FFmpeg transcode, cache eviction
  ivr/handlers.go         — Service-agnostic session API (create/digit/submit/delete)
  playout/playout.go      — State machine: filler → transition → playing → filler
  queue/queue.go          — Request queue with rate limiting and dedup
  ws/hub.go               — WebSocket hub with last-state replay for new clients
web/channel/              — React frontend (Vite)
configs/config.yaml       — Runtime config
```

## Playout State Machine

`filler` → `transition` → `playing` → (optional `ad_break`) → back to `filler` (or next in queue)

- **Filler** cycles between ident screen and catalogue scroll. After `filler_random_delay_minutes`, plays a random cached video.
- **Transition** shows "coming up" overlay for `transition_seconds`.
- **Playing** streams video. Safety timer advances queue if renderer doesn't report `video_ended`.
- **Ad break** plays a random sting from `ads_dir` between requested videos. See below.

## IVR Session API

All endpoints live under `/api/ivr/sessions`. Up to `ivr.MaxConcurrent` (default 3) concurrently-active sessions may be accepted at once; additional `POST` attempts return `429 Too Many Requests`. "Active" means the session is in `dialling` or `validated` — `success`/`fail` sessions linger for the on-screen result display but do not block new callers.

A session moves through four explicit states:

```
  dialling  -> caller is entering digits
  validated -> backend confirmed the code resolves to a catalogue
               entry and is waiting for the caller to press 1
               (confirm) or 2 (cancel). On-screen overlay keeps
               showing the digits; the artist/title are sent in
               the response body so the IVR can speak them on the
               phone, but the channel itself never flashes song
               details during the confirm step (matches the
               original Box behaviour).
  success   -> caller confirmed, request is on the queue. "Thanx!"
  fail      -> unknown code or rejected by the queue. "Try again"
```

| Method | Path                              | Purpose |
|--------|-----------------------------------|---------|
| POST   | `/api/ivr/sessions`               | Create session. Body `{caller_id?}`. Returns `{session_id, expires_in_seconds}`. |
| POST   | `/api/ivr/sessions/{id}/digit`    | Body `{digit: "5"}`. State-aware — see below. |
| POST   | `/api/ivr/sessions/{id}/submit`   | Finalise dialling early (validate current digits). Usually unnecessary because `/digit` auto-submits at 3 digits. |
| POST   | `/api/ivr/sessions/{id}/confirm`  | Commit a validated session to the queue. Transitions to `success`. |
| POST   | `/api/ivr/sessions/{id}/cancel`   | Clear digits and return to empty `dialling` so the caller can try again without hanging up. |
| DELETE | `/api/ivr/sessions/{id}`          | Caller hung up. |
| GET    | `/api/ivr/sessions/{id}`          | Inspect current state. |

**Digit dispatching** is state-aware so a dumb DTMF forwarder can drive the whole flow by POSTing every keypress to `/digit`:

- In `dialling`: `0`-`9` append, `#` submits, `*` clears digits.
- In `validated`: `1` confirms (same as `/confirm`), `2` or `*` cancels (same as `/cancel`), other digits are ignored.
- In `success`/`fail`: digits are ignored until the session is reaped.

Session TTLs:
- Dialling: 30s idle (`ivr.SessionTTL`).
- Validated: `ivr.confirm_ttl_seconds` (default 15s) — the IVR prompt plus thinking time must fit inside this.
- Success / fail: 4s linger so the overlay has time to render (`ivr.ResultLingerTime`).

A reaper goroutine sweeps expired sessions once per second.

### On-screen feedback

Every state change broadcasts a `dial_update` WebSocket event:

```json
{
  "type": "dial_update",
  "callers": [
    {"id": "a1", "digits": "10",  "status": "dialling"},
    {"id": "b2", "digits": "345", "status": "validated",
     "code": "345", "artist": "BLUR", "title": "Song 2"},
    {"id": "c3", "digits": "345", "status": "success",
     "code": "345", "artist": "BLUR", "title": "Song 2"},
    {"id": "d4", "digits": "999", "status": "fail"}
  ]
}
```

`RequestDigits.jsx` consumes this and renders a phone icon plus the entered digits, the validated selection, or the accept/reject text for each active caller. It takes over the bottom-ticker slot while any caller is active, matching the patent's FIG. 1 step 32 "DISPLAY SELECTION #" requirement.

### Writing an adapter

Any voice provider becomes a thin shim:

1. On inbound call → `POST /sessions`. If it returns 429, play "all lines busy" and hang up.
2. Play a greeting, then forward each DTMF keypress to `POST /sessions/{id}/digit`.
3. When the digit response comes back with `status: "validated"`, the response body contains `code`, `artist`, `title` — play "You chose X, <artist> — <title>, press 1 to confirm or 2 to cancel" and keep forwarding DTMF.
4. When the status flips to `success`, play "Thanx!" and hang up. On `fail`, play "Try again" and either loop back to step 2 or hang up.
5. On caller hangup at any point → `DELETE /sessions/{id}`.

None of that glue lives in this repo by design — keep provider-specific shapes at the edge. See the companion `retromusicbox-telephony` repo for a reference FreeSWITCH + Lua implementation.

## Ad Breaks (Stop Sets)

Patent-faithful "stop sets" between requested videos. Drop short `.mp4`/`.webm`/`.mov` station-ID stings into `assets/ads/` and one will play between every N requested videos (configured via `playout.ads_every_n_videos` in `configs/config.yaml`; set to `0` to disable).

- Served at `/ads/<filename>` by `cmd/rmbd/main.go`.
- Picked at random in `internal/playout/playout.go` `tryPlayAdBreak()`.
- Triggered from `advanceQueue()` only when there is a next queued video to come back to (no ad break before idling into filler).
- Ads broadcast a `play` WebSocket message with `video.is_ad: true`. The frontend hides `NowPlaying`, the code badge, and `BottomTicker` while `is_ad` is true; the `ChannelLogo` bug stays visible.
- Safety timer = `ad_max_seconds` (default 90s) in case `video_ended` doesn't fire.

## Common Tasks

**Add a video:** `./rmbctl add --youtube <YOUTUBE_ID>` or `POST /api/catalogue` with `{"youtube_id": "..."}`

**Request a video:** `POST /api/queue` with `{"code": "001"}`

**Operator-issued selection:** `./rmbctl request --code 001 [--now]` or `POST /api/queue/playnow` with `{"code": "001", "force": true}`. Bumps the code to the front of the queue (bypassing the per-caller rate limit). With `--now` / `force: true` it also calls `Skip()` so the channel cuts to it immediately. The CLI talks to a running `rmbd` over HTTP — defaults to `http://localhost:{server.port}`, override with `--api-url` or `RMBD_URL`.

**Pre-cache a video:** `POST /api/catalogue/001/cache`

**Skip current:** `POST /api/queue/skip`

**Simulate a phone call (curl):**
```bash
SID=$(curl -s -X POST localhost:8080/api/ivr/sessions | jq -r .session_id)
curl -s -X POST localhost:8080/api/ivr/sessions/$SID/digit -d '{"digit":"0"}'
curl -s -X POST localhost:8080/api/ivr/sessions/$SID/digit -d '{"digit":"0"}'
curl -s -X POST localhost:8080/api/ivr/sessions/$SID/digit -d '{"digit":"1"}'  # auto-submits
```

## Gotchas

- The frontend embed means you must rebuild both frontend AND backend after any React/CSS change: `cd web/channel && npm run build && cd ../.. && go build -o rmbd ./cmd/rmbd`
- Browser cache can be aggressive — hard refresh (`Cmd+Shift+R`) after rebuilds.
- SQLite single-writer: `db.SetMaxOpenConns(1)`. Don't try to parallelise writes.
- The `handleRequestPage` in `cmd/rmbd/main.go` is inline HTML via `fmt.Fprintf`, not a template. Phone number is injected from config.
- `box-logo.png` is kept as the asset filename for continuity — replace the file, not the path, when you want to swap artwork.
