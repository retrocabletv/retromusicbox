# The Box

A modern recreation of the iconic 1990s interactive music video TV channel. Viewers call a phone number, enter a catalogue number, and their requested music video plays on air with an on-screen digit display showing upcoming requests.

## Architecture

- **Go backend** (`boxd`) — single binary serving the API, WebSocket, media files, IVR webhooks, and embedded React app
- **React frontend** — full-screen channel renderer (video playback, digit display, filler screens)
- **SQLite** (WAL mode) — catalogue and request queue
- **yt-dlp + FFmpeg** — on-demand YouTube video fetching and transcoding
- **Jambonz** — IVR platform for phone-in requests

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 20+
- FFmpeg (with libx264, AAC)
- yt-dlp
- GCC (for SQLite CGo bindings)

### Build

```bash
# Build everything (React frontend + Go binaries)
make build
```

### Run

```bash
# Initialise the database
./boxctl init-db

# Add videos to the catalogue
./boxctl add --youtube "dQw4w9WgXcQ"
./boxctl add --youtube "y6120QOlsfU"

# Start the server
./boxd --config configs/config.yaml
```

### URLs

| URL | Description |
|-----|-------------|
| `http://localhost:8080/channel` | Full-screen channel output (for capture pipeline) |
| `http://localhost:8080/request` | Web request page |
| `http://localhost:8080/api/catalogue` | Catalogue API |
| `http://localhost:8080/api/queue` | Queue API |
| `http://localhost:8080/ws` | WebSocket (playout state) |

### Docker

```bash
docker build -t thebox .
docker run -p 8080:8080 -v $(pwd)/data:/app/data thebox
```

## CLI (`boxctl`)

```
boxctl init-db                    # Initialise the database
boxctl add --youtube <ID>         # Add a video by YouTube ID
boxctl remove --code <CODE>       # Remove by catalogue code
boxctl list                       # List all catalogue entries
boxctl search --query <QUERY>     # Search by artist/title
boxctl cache-all                  # Fetch and transcode all videos
```

## Chrome Capture Pipeline

Point the existing Docker capture pipeline at `http://localhost:8080/channel` with:

```
--autoplay-policy=no-user-gesture-required
```

## Configuration

See `configs/config.yaml` for all options.
