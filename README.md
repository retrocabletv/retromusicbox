# retromusicbox

A modern recreation of the 1990s interactive music video TV channel format. Viewers enter a 3-digit catalogue code, via the web request page or a phone front-end, and the requested music video is queued for the full-screen channel output. The channel renders the video, request digit feedback, logo bug, catalogue/upcoming ticker, filler screens, and optional stop-set adverts.

## Related projects

- [retromusicbox-telephony](https://github.com/retrocabletv/retromusicbox-telephony) - FreeSWITCH + Lua voice front-end for the service-agnostic IVR API in this repo. Use it when you want callers to dial in and enter DTMF codes over SIP/PSTN.
- [webpagestreamer](https://github.com/retrocabletv/webpagestreamer) - Dockerized Chromium capture pipeline. Point it at `/channel` to turn the browser-rendered channel into MPEG-TS for IPTV, cable-lab, or downstream streaming workflows.

## Architecture

- **Go backend** (`rmbd`) - single binary serving API, WebSocket, media files, IVR session API, ad assets, request page, and embedded React channel app.
- **React frontend** (`web/channel/`) - full-screen channel renderer. The backend playout controller owns state; the frontend renders WebSocket events.
- **SQLite** (WAL mode) - catalogue and request queue, with a single writer connection.
- **yt-dlp + FFmpeg** - YouTube metadata, thumbnail download, video fetching, loudness normalization, and transcoding.
- **Service-agnostic IVR** - REST session API that can be driven by FreeSWITCH, Asterisk, Twilio, Jambonz, or a DIY DTMF adapter.

## Quick start

The fastest path is the pre-built container image.

### Docker Compose

```bash
# Pulls ghcr.io/retrocabletv/retromusicbox:latest, mounts ./data and
# ./configs/config.yaml read-only, exposes :8080.
docker compose up -d

# Pin a specific release
RMBD_TAG=0.6.0 docker compose up -d
```

The image is multi-arch (`linux/amd64`, `linux/arm64`). Tagged releases also publish static Linux binaries; see [Releases](https://github.com/retrocabletv/retromusicbox/releases).

Bootstrap the catalogue inside the running container:

```bash
docker compose exec rmbd ./rmbctl init-db
docker compose exec rmbd ./rmbctl add --youtube "dQw4w9WgXcQ"
docker compose exec rmbd ./rmbctl add --youtube "y6120QOlsfU"
```

### Build from source

Prerequisites: Go 1.22+, Node.js 20+, FFmpeg with libx264/AAC support, yt-dlp, and GCC for SQLite CGo bindings.

```bash
make build

./rmbctl init-db
./rmbctl add --youtube "dQw4w9WgXcQ"
./rmbd --config configs/config.yaml
```

Frontend assets are embedded in `rmbd`, so rebuild both pieces after React/CSS changes:

```bash
cd web/channel && npm run build
cd ../..
go build -o rmbd ./cmd/rmbd
```

To build the container image locally instead of pulling:

```bash
docker compose up -d --build
```

## URLs

| URL | Description |
| --- | --- |
| `http://localhost:8080/channel` | Full-screen channel output for display or capture |
| `http://localhost:8080/request` | Web request page |
| `http://localhost:8080/api/catalogue` | Catalogue API |
| `http://localhost:8080/api/queue` | Queue API |
| `http://localhost:8080/api/ivr/sessions` | IVR session API |
| `http://localhost:8080/ws` | WebSocket playout state |

## CLI (`rmbctl`)

```bash
rmbctl init-db                              # Initialise the database
rmbctl add --youtube <ID>                   # Add one video by YouTube ID
rmbctl add --playlist <URL>                 # Add every video from a playlist
rmbctl edit --code <CODE> --artist <A>      # Edit catalogue metadata
rmbctl remove --code <CODE>                 # Remove by catalogue code
rmbctl list                                 # List all catalogue entries
rmbctl search --query <QUERY>               # Search by artist/title
rmbctl cache-all                            # Fetch and transcode all videos
rmbctl refresh                              # Refresh metadata and 3-digit codes
rmbctl request --code <CODE> [--now]        # Operator request; --now preempts playback
```

`rmbctl request` talks to a running `rmbd` over HTTP. It defaults to `http://localhost:{server.port}` and can be overridden with `--api-url` or `RMBD_URL`.

## IVR and telephony

`rmbd` intentionally does not contain SIP, RTP, carrier webhook, or telephony-provider code. It exposes a compact REST session API; adapters translate caller DTMF into these calls.

The maintained FreeSWITCH adapter lives in [retromusicbox-telephony](https://github.com/retrocabletv/retromusicbox-telephony). That project answers calls, plays prompts, forwards digits to `rmbd`, handles confirm/cancel, and cleans up sessions on hangup.

Session endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/ivr/sessions` | Create a session. Returns `{session_id, expires_in_seconds}`; returns `429` if all caller slots are busy. |
| `POST` | `/api/ivr/sessions/{id}/digit` | Send one DTMF key. State-aware, so a simple adapter can forward every keypress here. |
| `POST` | `/api/ivr/sessions/{id}/submit` | Finalise dialling early. Usually unnecessary because 3 digits auto-submit. |
| `POST` | `/api/ivr/sessions/{id}/confirm` | Commit a validated code to the request queue. |
| `POST` | `/api/ivr/sessions/{id}/cancel` | Clear digits and return the same session to `dialling`. |
| `GET` | `/api/ivr/sessions/{id}` | Inspect session state. |
| `DELETE` | `/api/ivr/sessions/{id}` | Caller hung up / cleanup. |

States:

```text
dialling  -> caller is entering digits
validated -> code exists; waiting for 1 to confirm or 2/* to cancel
success   -> request accepted and queued
fail      -> unknown code or rejected by queue policy
```

Digit handling is state-aware:

- In `dialling`, `0`-`9` append, `#` submits, and `*` clears.
- In `validated`, `1` confirms; `2` or `*` cancels; other keys are ignored.
- In `success` or `fail`, digits are ignored until the result display expires.

Every state change broadcasts a `dial_update` WebSocket event. The channel uses it to show concurrent callers, entered digits, validation, and accept/reject feedback on screen.

## Chrome capture pipeline

The channel is a browser-rendered page. For a video stream output, run [webpagestreamer](https://github.com/retrocabletv/webpagestreamer) and point it at `/channel`.

Example TCP test stream from Docker on the same host:

```bash
git clone https://github.com/retrocabletv/webpagestreamer.git
cd webpagestreamer
docker build -t webpagestreamer .

docker run --rm -p 9876:9876 \
  -e URL="http://host.docker.internal:8080/channel" \
  -e OUTPUT="tcp://0.0.0.0:9876?listen=1" \
  -e PROFILE="pal" \
  webpagestreamer
```

Then receive it with:

```bash
ffplay -fflags nobuffer -flags low_delay -f mpegts tcp://127.0.0.1:9876
```

When driving Chromium yourself, use:

```text
--autoplay-policy=no-user-gesture-required
```

This lets the channel play audio without a user gesture in headless or kiosk capture environments.

## Ad breaks

Drop short `.mp4`, `.webm`, or `.mov` station-ID/ad stings into `assets/ads/`. `rmbd` serves them from `/ads/<filename>` and can insert one between requested videos.

Relevant `configs/config.yaml` options:

```yaml
playout:
  ads_dir: "assets/ads"
  ads_every_n_videos: 2   # set to 0 to disable
  ad_max_seconds: 90
```

Ads are only inserted when there is another requested video to return to; the channel does not play an ad before idling into filler.

## Requesting videos over HTTP

Queue a normal request:

```bash
curl -s -X POST http://localhost:8080/api/queue \
  -H 'Content-Type: application/json' \
  -d '{"code":"001"}'
```

Operator play-now request:

```bash
curl -s -X POST http://localhost:8080/api/queue/playnow \
  -H 'Content-Type: application/json' \
  -d '{"code":"001","force":true}'
```

Pre-cache a catalogue entry:

```bash
curl -s -X POST http://localhost:8080/api/catalogue/001/cache
```

Skip the current item:

```bash
curl -s -X POST http://localhost:8080/api/queue/skip
```

## Configuration

See [`configs/config.yaml`](configs/config.yaml) for runtime options. Common settings include:

- `server.port` - HTTP listen port.
- `channel.phone_number_display` - text shown on the web request page and channel UI.
- `channel.crt_enabled` - scanline/vignette CRT treatment for the rendered channel.
- `fetcher.yt_dlp_path` - path or executable name for `yt-dlp`.
- `fetcher.prefetch_threshold` - number of upcoming queue items to keep fetched/transcoded.
- `ivr.confirm_ttl_seconds` - how long callers have to confirm a valid selection.
- `queue.empty_queue_action` - random cached video filler when the request queue is empty.

## Notes

- Catalogue codes are 3 digits (`001`-`999`).
- Replace `box-logo.png` with your own channel artwork before public use.
- Vite outputs embedded frontend assets to `cmd/rmbd/static/` with `assetsDir: 'static'` to avoid colliding with the backend `/assets/` route.
- Browser caches can hold old channel assets after a rebuild; hard-refresh the channel page if changes do not appear.
