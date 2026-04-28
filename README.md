# retromusicbox

A modern recreation of the 1990s interactive music video TV channel format. Viewers call a phone number (or use a web page), enter a 3-digit catalogue code, and their requested music video plays on air with an on-screen digit display showing upcoming requests.

## Architecture

- **Go backend** (`rmbd`) — single binary serving the API, WebSocket, media files, IVR session API, and embedded React app
- **React frontend** — full-screen channel renderer (video playback, digit display, filler screens)
- **SQLite** (WAL mode) — catalogue and request queue
- **yt-dlp + FFmpeg** — on-demand YouTube video fetching and transcoding
- **Service-agnostic IVR** — simple session REST API; plug in Jambonz, Twilio, or anything else

## Quick Start

The fastest path is the pre-built container image — no toolchain needed locally.

### Run with Docker Compose (recommended)

```bash
# Pulls ghcr.io/retrocabletv/retromusicbox:latest, mounts ./data and
# ./configs/config.yaml read-only, exposes :8080.
docker compose up -d

# Pin a specific version
RMBD_TAG=0.6.0 docker compose up -d
```

The image is multi-arch (`linux/amd64`, `linux/arm64`). Each tagged release also publishes static Linux binaries — see [Releases](https://github.com/retrocabletv/retromusicbox/releases) for `rmbd-linux-amd64-musl`, `rmbctl-linux-amd64-musl`, and `SHA256SUMS-linux-amd64-musl.txt`.

Bootstrap the catalogue inside the running container:

```bash
docker compose exec rmbd ./rmbctl init-db
docker compose exec rmbd ./rmbctl add --youtube "dQw4w9WgXcQ"
docker compose exec rmbd ./rmbctl add --youtube "y6120QOlsfU"
```

### Build from source

Prerequisites: Go 1.22+, Node.js 20+, FFmpeg (with libx264, AAC), yt-dlp, GCC (for SQLite CGo bindings).

```bash
# React frontend + Go binaries
make build

./rmbctl init-db
./rmbctl add --youtube "dQw4w9WgXcQ"
./rmbd --config configs/config.yaml
```

To build the container image locally instead of pulling: `docker compose up -d --build`.

### URLs

| URL | Description |
|-----|-------------|
| `http://localhost:8080/channel` | Full-screen channel output (for capture pipeline) |
| `http://localhost:8080/request` | Web request page |
| `http://localhost:8080/api/catalogue` | Catalogue API |
| `http://localhost:8080/api/queue` | Queue API |
| `http://localhost:8080/api/ivr/sessions` | IVR session API |
| `http://localhost:8080/ws` | WebSocket (playout state) |

## CLI (`rmbctl`)

```
rmbctl init-db                    # Initialise the database
rmbctl add --youtube <ID>         # Add a video by YouTube ID
rmbctl remove --code <CODE>       # Remove by catalogue code
rmbctl list                       # List all catalogue entries
rmbctl search --query <QUERY>     # Search by artist/title
rmbctl cache-all                  # Fetch and transcode all videos
```

## IVR

The backend exposes a small service-agnostic session API. Any IVR provider (Jambonz, Twilio, Asterisk, a Raspberry Pi DTMF decoder) can drive it with four REST calls:

```
POST   /api/ivr/sessions                 # create session, returns {session_id}
POST   /api/ivr/sessions/{id}/digit      # body: {"digit": "5"}
POST   /api/ivr/sessions/{id}/submit     # finalise (optional — auto-fires at 3 digits)
DELETE /api/ivr/sessions/{id}            # caller hung up
```

Up to 3 sessions may be active concurrently. Each session broadcasts `dial_update` WebSocket messages so the channel overlay shows the call icon, live digit entry, and accept/reject feedback (patent step 32 — "DISPLAY SELECTION #").

## Chrome Capture Pipeline

Point the existing Docker capture pipeline at `http://localhost:8080/channel` with:

```
--autoplay-policy=no-user-gesture-required
```

## Configuration

See `configs/config.yaml` for all options.
