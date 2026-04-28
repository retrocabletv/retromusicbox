package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/alexkinch/retromusicbox/internal/catalogue"
	"github.com/alexkinch/retromusicbox/internal/config"
	"github.com/alexkinch/retromusicbox/internal/db"
	"github.com/alexkinch/retromusicbox/internal/fetcher"
	"github.com/alexkinch/retromusicbox/internal/ivr"
	"github.com/alexkinch/retromusicbox/internal/playout"
	"github.com/alexkinch/retromusicbox/internal/queue"
	"github.com/alexkinch/retromusicbox/internal/ws"
)

//go:embed static/*
var staticFiles embed.FS

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Open database
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Create services
	catService := catalogue.NewService(database)
	queueService := queue.NewService(database, cfg.Queue.MaxRequestsPerCallerPerHour, cfg.Queue.AllowDuplicateInQueue)

	// Reset any requests stuck from a previous unclean shutdown
	if err := queueService.ResetStale(); err != nil {
		log.Printf("Warning: failed to reset stale requests: %v", err)
	}
	hub := ws.NewHub()
	fetcherService := fetcher.NewService(cfg.Fetcher, catService, queueService)
	controller := playout.NewController(cfg.Playout, cfg.Channel, catService, queueService, fetcherService, hub)

	// Wire up callbacks
	fetcherService.SetOnReady(func(code string) {
		controller.NotifyQueueChange()
	})

	// Update yt-dlp on startup
	go fetcherService.UpdateYtDlp()

	// Start background workers
	stopCh := make(chan struct{})
	defer close(stopCh)
	go fetcherService.StartPrefetchWorker(stopCh)

	// Start playout controller
	controller.Start()
	defer controller.Stop()

	// Set up HTTP routes
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("POST /api/catalogue", handleAddCatalogue(catService, fetcherService))
	mux.HandleFunc("GET /api/catalogue", handleListCatalogue(catService))
	mux.HandleFunc("GET /api/catalogue/search", handleSearchCatalogue(catService))
	mux.HandleFunc("GET /api/catalogue/{code}", handleGetCatalogue(catService))
	mux.HandleFunc("PATCH /api/catalogue/{code}", handleUpdateCatalogue(catService))
	mux.HandleFunc("DELETE /api/catalogue/{code}", handleDeleteCatalogue(catService))
	mux.HandleFunc("POST /api/catalogue/{code}/cache", handleCacheVideo(catService, fetcherService))

	mux.HandleFunc("POST /api/queue", handleAddQueue(queueService, catService, controller))
	mux.HandleFunc("GET /api/queue", handleGetQueue(queueService))
	mux.HandleFunc("DELETE /api/queue/{id}", handleDeleteQueue(queueService, controller))
	mux.HandleFunc("POST /api/queue/skip", handleSkip(controller))
	mux.HandleFunc("POST /api/queue/playnow", handlePlayNow(queueService, catService, fetcherService, controller))

	// Frontend config (client-shaped subset; not the full server Config)
	mux.HandleFunc("GET /api/config", handleConfig(cfg.Channel))

	// WebSocket
	mux.HandleFunc("GET /ws", hub.HandleWebSocket)

	// Media files
	mediaFS := http.StripPrefix("/media/", http.FileServer(http.Dir(cfg.Fetcher.ReadyDir)))
	mux.Handle("GET /media/", mediaFS)

	thumbFS := http.StripPrefix("/media/thumbnails/", http.FileServer(http.Dir(cfg.Fetcher.ThumbnailDir)))
	mux.Handle("GET /media/thumbnails/", thumbFS)

	// IVR session API (service-agnostic — any DTMF/voice front-end can drive it)
	if cfg.IVR.Enabled {
		ivrHandler := ivr.NewHandler(catService, queueService, hub, func() {
			controller.NotifyQueueChange()
		}, time.Duration(cfg.IVR.ConfirmTTLSeconds)*time.Second)
		ivrHandler.Register(mux)
	}

	// Static assets (jingles etc)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))

	// Stop-set / station ID stings (ad break videos)
	if cfg.Playout.AdsDir != "" {
		mux.Handle("GET /ads/", http.StripPrefix("/ads/", http.FileServer(http.Dir(cfg.Playout.AdsDir))))
	}

	// Request page
	mux.HandleFunc("GET /request", handleRequestPage(cfg.Channel))

	// React channel app (embedded)
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("Failed to get static sub-fs: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticSub))
	mux.HandleFunc("GET /channel", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
	mux.Handle("GET /", fileServer)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("retromusicbox is live on http://localhost%s", addr)
	log.Printf("Channel: http://localhost%s/channel", addr)
	log.Printf("Request page: http://localhost%s/request", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// API Handlers

type addCatalogueRequest struct {
	YoutubeID   string `json:"youtube_id"`
	PlaylistURL string `json:"playlist_url"`
}

type playlistFailure struct {
	YoutubeID string `json:"youtube_id"`
	Error     string `json:"error"`
}

type playlistImportResult struct {
	Total   int                `json:"total"`
	Added   []*catalogue.Entry `json:"added"`
	Skipped []string           `json:"skipped"`
	Failed  []playlistFailure  `json:"failed"`
}

type updateCatalogueRequest struct {
	Title  *string `json:"title"`
	Artist *string `json:"artist"`
}

func handleAddCatalogue(cat *catalogue.Service, fetch *fetcher.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req addCatalogueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.YoutubeID == "" && req.PlaylistURL == "" {
			httpError(w, "youtube_id or playlist_url is required", http.StatusBadRequest)
			return
		}

		if req.PlaylistURL != "" {
			ids, err := fetch.FetchPlaylistVideoIDs(req.PlaylistURL)
			if err != nil {
				httpError(w, fmt.Sprintf("Failed to list playlist: %v", err), http.StatusBadGateway)
				return
			}
			result := playlistImportResult{Total: len(ids), Added: []*catalogue.Entry{}, Skipped: []string{}, Failed: []playlistFailure{}}
			for _, id := range ids {
				entry, err := importVideo(cat, fetch, id)
				if err != nil {
					if err == errAlreadyInCatalogue {
						result.Skipped = append(result.Skipped, id)
						continue
					}
					result.Failed = append(result.Failed, playlistFailure{YoutubeID: id, Error: err.Error()})
					continue
				}
				result.Added = append(result.Added, entry)
			}
			w.WriteHeader(http.StatusCreated)
			jsonResponse(w, result)
			return
		}

		entry, err := importVideo(cat, fetch, req.YoutubeID)
		if err == errAlreadyInCatalogue {
			jsonResponse(w, entry)
			return
		}
		if err != nil {
			httpError(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
		jsonResponse(w, entry)
	}
}

var errAlreadyInCatalogue = fmt.Errorf("already in catalogue")

func importVideo(cat *catalogue.Service, fetch *fetcher.Service, youtubeID string) (*catalogue.Entry, error) {
	if existing, _ := cat.GetByYoutubeID(youtubeID); existing != nil {
		return existing, errAlreadyInCatalogue
	}
	info, err := fetch.FetchVideoInfo(youtubeID)
	if err != nil {
		return nil, fmt.Errorf("fetch video info: %w", err)
	}
	thumbPath, _ := fetch.DownloadThumbnail(youtubeID, info.Thumbnail)
	artist, title := info.CleanTitle()
	entry, err := cat.Add(youtubeID, title, artist, int(info.Duration), thumbPath)
	if err != nil {
		return nil, fmt.Errorf("add to catalogue: %w", err)
	}
	return entry, nil
}

func handleListCatalogue(cat *catalogue.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		entries, err := cat.List(limit, offset)
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []catalogue.Entry{}
		}
		jsonResponse(w, entries)
	}
}

func handleSearchCatalogue(cat *catalogue.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			httpError(w, "q parameter required", http.StatusBadRequest)
			return
		}
		entries, err := cat.Search(q)
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entries == nil {
			entries = []catalogue.Entry{}
		}
		jsonResponse(w, entries)
	}
}

func handleGetCatalogue(cat *catalogue.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		entry, err := cat.GetByCode(code)
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if entry == nil {
			httpError(w, "Not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, entry)
	}
}

func handleUpdateCatalogue(cat *catalogue.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")

		entry, err := cat.GetByCode(code)
		if err != nil || entry == nil {
			httpError(w, "Not found", http.StatusNotFound)
			return
		}

		var req updateCatalogueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		title := entry.Title
		artist := entry.Artist
		if req.Title != nil {
			title = *req.Title
		}
		if req.Artist != nil {
			artist = *req.Artist
		}

		if err := cat.Update(code, title, artist); err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		entry.Title = title
		entry.Artist = artist
		jsonResponse(w, entry)
	}
}

func handleDeleteCatalogue(cat *catalogue.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if err := cat.Delete(code); err != nil {
			httpError(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCacheVideo(cat *catalogue.Service, fetch *fetcher.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		entry, err := cat.GetByCode(code)
		if err != nil || entry == nil {
			httpError(w, "Not found", http.StatusNotFound)
			return
		}

		go func() {
			if err := fetch.FetchAndTranscode(entry.YoutubeID); err != nil {
				log.Printf("Cache failed for %s: %v", code, err)
			}
		}()

		jsonResponse(w, map[string]string{"status": "caching", "code": code})
	}
}

type addQueueRequest struct {
	Code     string `json:"code"`
	CallerID string `json:"caller_id,omitempty"`
}

func handleAddQueue(q *queue.Service, cat *catalogue.Service, ctrl *playout.Controller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req addQueueRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Code == "" {
			httpError(w, "code is required", http.StatusBadRequest)
			return
		}

		entry, err := cat.GetByCode(req.Code)
		if err != nil || entry == nil {
			httpError(w, "Catalogue code not found", http.StatusNotFound)
			return
		}

		callerID := req.CallerID
		if callerID == "" {
			callerID = "web"
		}

		request, position, err := q.Add(req.Code, callerID)
		if err != nil {
			httpError(w, err.Error(), http.StatusTooManyRequests)
			return
		}

		ctrl.NotifyQueueChange()

		jsonResponse(w, map[string]interface{}{
			"request":  request,
			"position": position,
			"title":    entry.Title,
			"artist":   entry.Artist,
		})
	}
}

func handleGetQueue(q *queue.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requests, err := q.GetActive()
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if requests == nil {
			requests = []queue.Request{}
		}
		jsonResponse(w, requests)
	}
}

func handleDeleteQueue(q *queue.Service, ctrl *playout.Controller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			httpError(w, "Invalid ID", http.StatusBadRequest)
			return
		}
		if err := q.Delete(id); err != nil {
			httpError(w, err.Error(), http.StatusNotFound)
			return
		}
		ctrl.NotifyQueueChange()
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleSkip(ctrl *playout.Controller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctrl.Skip()
		jsonResponse(w, map[string]string{"status": "skipped"})
	}
}

type playNowRequest struct {
	Code     string `json:"code"`
	CallerID string `json:"caller_id,omitempty"`
	Force    bool   `json:"force,omitempty"`
}

// handlePlayNow is the operator-facing "select this and play it" endpoint
// behind `rmbctl request`. It bumps the chosen catalogue code to the front of
// the queue (bypassing the per-caller rate limit). With force=true it also
// preempts whatever is currently playing.
func handlePlayNow(q *queue.Service, cat *catalogue.Service, fetch *fetcher.Service, ctrl *playout.Controller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req playNowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if req.Code == "" {
			httpError(w, "code is required", http.StatusBadRequest)
			return
		}

		entry, err := cat.GetByCode(req.Code)
		if err != nil || entry == nil {
			httpError(w, "Catalogue code not found", http.StatusNotFound)
			return
		}

		callerID := req.CallerID
		if callerID == "" {
			callerID = "operator"
		}

		request, _, err := q.AddBypassRateLimit(req.Code, callerID)
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := q.Prioritise(request.ID); err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Kick off a fetch if the file isn't already cached, so it can play as
		// soon as the controller picks it up. The prefetch worker would do
		// this on its next 5s tick anyway, but with --now we want it sooner.
		if !fetch.IsReady(entry.YoutubeID) {
			go func(ytID, code string) {
				if err := fetch.FetchAndTranscode(ytID); err != nil {
					log.Printf("[playnow] fetch failed for %s: %v", code, err)
				}
			}(entry.YoutubeID, req.Code)
		}

		if req.Force {
			ctrl.Skip()
		} else {
			ctrl.NotifyQueueChange()
		}

		jsonResponse(w, map[string]interface{}{
			"request": request,
			"title":   entry.Title,
			"artist":  entry.Artist,
			"forced":  req.Force,
		})
	}
}

// clientConfig is the frontend-shaped subset of Config exposed via /api/config.
// Server-only fields (paths, db, fetcher internals) are deliberately omitted.
type clientConfig struct {
	Channel clientChannelConfig `json:"channel"`
}

type clientChannelConfig struct {
	Width              int    `json:"width"`
	Height             int    `json:"height"`
	PhoneNumberDisplay string `json:"phone_number_display"`
	CRTEnabled         bool   `json:"crt_enabled"`
}

func handleConfig(channelCfg config.ChannelConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, clientConfig{
			Channel: clientChannelConfig{
				Width:              channelCfg.Width,
				Height:             channelCfg.Height,
				PhoneNumberDisplay: channelCfg.PhoneNumberDisplay,
				CRTEnabled:         channelCfg.CRTEnabled,
			},
		})
	}
}

func handleRequestPage(channelCfg config.ChannelConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>RETROMUSICBOX - Request a Video</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { background: #000; color: #fff; font-family: 'Courier New', monospace; display: flex; justify-content: center; align-items: center; min-height: 100vh; }
  .container { text-align: center; max-width: 500px; padding: 2rem; }
  h1 { color: #FFD700; font-size: 3rem; margin-bottom: 0.5rem; text-shadow: 0 0 20px rgba(255,215,0,0.5); }
  .subtitle { color: #00FFFF; margin-bottom: 2rem; }
  .form-group { margin-bottom: 1.5rem; }
  input[type="text"] { background: #111; border: 2px solid #FFD700; color: #FFD700; font-family: 'Courier New', monospace; font-size: 2rem; padding: 0.5rem 1rem; text-align: center; width: 200px; letter-spacing: 0.5rem; }
  input[type="text"]::placeholder { color: #555; letter-spacing: 0.2rem; font-size: 1rem; }
  button { background: #FFD700; color: #000; border: none; padding: 0.75rem 2rem; font-family: 'Courier New', monospace; font-size: 1.2rem; font-weight: bold; cursor: pointer; text-transform: uppercase; }
  button:hover { background: #00FFFF; }
  .result { margin-top: 1.5rem; padding: 1rem; min-height: 2rem; }
  .result.success { color: #00FF00; }
  .result.error { color: #FF4444; }
  .phone { color: #00FFFF; margin-top: 2rem; font-size: 0.9rem; }
</style>
</head>
<body>
<div class="container">
  <h1>RETROMUSICBOX</h1>
  <p class="subtitle">REQUEST A VIDEO</p>
  <div class="form-group">
    <input type="text" id="code" maxlength="3" placeholder="000" pattern="\d{3}">
  </div>
  <button onclick="submitRequest()">REQUEST</button>
  <div id="result" class="result"></div>
  <p class="phone">Or call %s</p>
</div>
<script>
async function submitRequest() {
  const code = document.getElementById('code').value.trim();
  const result = document.getElementById('result');
  if (!/^\d{3}$/.test(code)) { result.className='result error'; result.textContent='Enter a 3-digit code'; return; }
  try {
    const resp = await fetch('/api/queue', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({code})});
    const data = await resp.json();
    if (!resp.ok) { result.className='result error'; result.textContent=data.error||'Request failed'; return; }
    result.className='result success';
    result.textContent='"'+data.title+'" by '+data.artist+' — #'+data.position+' in queue';
    document.getElementById('code').value='';
  } catch(e) { result.className='result error'; result.textContent='Connection error'; }
}
document.getElementById('code').addEventListener('keydown', function(e) { if(e.key==='Enter') submitRequest(); });
</script>
</body>
</html>`, channelCfg.PhoneNumberDisplay)
	}
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func httpError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
