package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"text/tabwriter"
	"time"

	"github.com/alexkinch/retromusicbox/internal/catalogue"
	"github.com/alexkinch/retromusicbox/internal/config"
	"github.com/alexkinch/retromusicbox/internal/db"
	"github.com/alexkinch/retromusicbox/internal/fetcher"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg := loadConfig()
	command := os.Args[1]

	switch command {
	case "init-db":
		initDB(cfg)
	case "add":
		addVideo(cfg)
	case "edit":
		editVideo(cfg)
	case "remove":
		removeVideo(cfg)
	case "list":
		listVideos(cfg)
	case "search":
		searchVideos(cfg)
	case "cache-all":
		cacheAll(cfg)
	case "refresh":
		refreshMetadata(cfg)
	case "request":
		requestVideo(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("rmbctl - retromusicbox catalogue manager")
	fmt.Println()
	fmt.Println("Usage: rmbctl <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  init-db                          Initialise the database")
	fmt.Println("  add --youtube <ID>               Add a video by YouTube ID")
	fmt.Println("  add --playlist <URL>             Add every video from a YouTube playlist URL")
	fmt.Println("  edit --code <CODE> [--artist <A>] [--title <T>]")
	fmt.Println("                                   Edit artist/title for a catalogue entry")
	fmt.Println("  remove --code <CODE>             Remove a video by catalogue code")
	fmt.Println("  list                             List all catalogue entries")
	fmt.Println("  search --query <QUERY>           Search by artist/title")
	fmt.Println("  cache-all                        Fetch and transcode all catalogue videos")
	fmt.Println("  refresh                          Re-fetch metadata from YouTube and reindex codes to 3-digit")
	fmt.Println("  request --code <CODE> [--now] [--api-url <URL>]")
	fmt.Println("                                   Make an operator selection. Bumps the code to the front")
	fmt.Println("                                   of the queue. With --now, also preempts the currently")
	fmt.Println("                                   playing video. Talks to a running rmbd over HTTP.")
}

func loadConfig() *config.Config {
	configPath := "configs/config.yaml"
	if envPath := os.Getenv("RMB_CONFIG"); envPath != "" {
		configPath = envPath
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", configPath, err)
	}
	return cfg
}

func initDB(cfg *config.Config) {
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	database.Close()
	fmt.Printf("Database initialised at %s\n", cfg.Database.Path)
}

func addVideo(cfg *config.Config) {
	ytID := ""
	playlistURL := ""
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--youtube":
			if i+1 < len(os.Args) {
				ytID = os.Args[i+1]
				i++
			}
		case "--playlist":
			if i+1 < len(os.Args) {
				playlistURL = os.Args[i+1]
				i++
			}
		}
	}
	if ytID == "" && playlistURL == "" {
		log.Fatal("Usage: rmbctl add --youtube <YOUTUBE_ID> | --playlist <PLAYLIST_URL>")
	}

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)
	fetch := fetcher.NewService(cfg.Fetcher, cat, nil)

	if playlistURL != "" {
		fmt.Printf("Listing playlist %s...\n", playlistURL)
		ids, err := fetch.FetchPlaylistVideoIDs(playlistURL)
		if err != nil {
			log.Fatalf("Failed to list playlist: %v", err)
		}
		fmt.Printf("Found %d videos.\n", len(ids))
		added, skipped, failed := 0, 0, 0
		for i, id := range ids {
			fmt.Printf("[%d/%d] %s: ", i+1, len(ids), id)
			status := addOne(cat, fetch, id)
			fmt.Println(status.msg)
			switch status.kind {
			case addOK:
				added++
			case addSkip:
				skipped++
			case addFail:
				failed++
			}
		}
		fmt.Printf("\nDone. Added %d, skipped %d, failed %d.\n", added, skipped, failed)
		return
	}

	fmt.Printf("Fetching info for %s...\n", ytID)
	status := addOne(cat, fetch, ytID)
	fmt.Println(status.msg)
	if status.kind == addFail {
		os.Exit(1)
	}
}

type addResultKind int

const (
	addOK addResultKind = iota
	addSkip
	addFail
)

type addResult struct {
	kind addResultKind
	msg  string
}

func addOne(cat *catalogue.Service, fetch *fetcher.Service, ytID string) addResult {
	if existing, _ := cat.GetByYoutubeID(ytID); existing != nil {
		return addResult{addSkip, fmt.Sprintf("already in catalogue as [%s] %s - %s", existing.Code, existing.Artist, existing.Title)}
	}
	info, err := fetch.FetchVideoInfo(ytID)
	if err != nil {
		return addResult{addFail, fmt.Sprintf("yt-dlp failed: %v", err)}
	}
	artist, title := info.CleanTitle()
	thumbPath, _ := fetch.DownloadThumbnail(ytID, info.Thumbnail)
	entry, err := cat.Add(ytID, title, artist, int(info.Duration), thumbPath)
	if err != nil {
		return addResult{addFail, fmt.Sprintf("failed to add: %v", err)}
	}
	return addResult{addOK, fmt.Sprintf("added [%s] %s - %s (%ds)", entry.Code, entry.Artist, entry.Title, derefInt(entry.DurationSeconds))}
}

func editVideo(cfg *config.Config) {
	code := ""
	artist := ""
	title := ""
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--code":
			if i+1 < len(os.Args) {
				code = os.Args[i+1]
				i++
			}
		case "--artist":
			if i+1 < len(os.Args) {
				artist = os.Args[i+1]
				i++
			}
		case "--title":
			if i+1 < len(os.Args) {
				title = os.Args[i+1]
				i++
			}
		}
	}
	if code == "" {
		log.Fatal("Usage: rmbctl edit --code <CODE> [--artist <ARTIST>] [--title <TITLE>]")
	}
	if artist == "" && title == "" {
		log.Fatal("At least one of --artist or --title is required")
	}

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)

	entry, err := cat.GetByCode(code)
	if err != nil || entry == nil {
		log.Fatalf("Catalogue entry %s not found", code)
	}

	if artist == "" {
		artist = entry.Artist
	}
	if title == "" {
		title = entry.Title
	}

	if err := cat.Update(code, title, artist); err != nil {
		log.Fatalf("Failed to update: %v", err)
	}

	fmt.Printf("Updated: [%s] %s - %s\n", code, artist, title)
}

func removeVideo(cfg *config.Config) {
	code := ""
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--code" && i+1 < len(os.Args) {
			code = os.Args[i+1]
			break
		}
	}
	if code == "" {
		log.Fatal("Usage: rmbctl remove --code <CODE>")
	}

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)
	if err := cat.Delete(code); err != nil {
		log.Fatalf("Failed to remove: %v", err)
	}
	fmt.Printf("Removed catalogue entry %s\n", code)
}

func listVideos(cfg *config.Config) {
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)
	entries, err := cat.GetAll()
	if err != nil {
		log.Fatalf("Failed to list: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CODE\tARTIST\tTITLE\tDURATION\tCACHED")
	for _, e := range entries {
		cached := "no"
		if e.VideoPath != nil && *e.VideoPath != "" {
			cached = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%ds\t%s\n", e.Code, e.Artist, e.Title, derefInt(e.DurationSeconds), cached)
	}
	w.Flush()
}

func searchVideos(cfg *config.Config) {
	query := ""
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--query" && i+1 < len(os.Args) {
			query = os.Args[i+1]
			break
		}
	}
	if query == "" {
		log.Fatal("Usage: rmbctl search --query <QUERY>")
	}

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)
	entries, err := cat.Search(query)
	if err != nil {
		log.Fatalf("Failed to search: %v", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CODE\tARTIST\tTITLE")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Code, e.Artist, e.Title)
	}
	w.Flush()
}

func cacheAll(cfg *config.Config) {
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)
	entries, err := cat.GetAll()
	if err != nil {
		log.Fatalf("Failed to list: %v", err)
	}

	fmt.Printf("Caching %d videos...\n", len(entries))
	for _, e := range entries {
		fmt.Printf("[%s] %s - %s: ", e.Code, e.Artist, e.Title)
		// Trigger fetch via API would be better, but for CLI we shell out
		cmd := exec.Command(cfg.Fetcher.YtDlpPath,
			"-f", fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]", cfg.Fetcher.MaxResolution, cfg.Fetcher.MaxResolution),
			"--merge-output-format", "mkv",
			"-o", fmt.Sprintf("%s/%s.mkv", cfg.Fetcher.CacheDir, e.YoutubeID),
			fmt.Sprintf("https://www.youtube.com/watch?v=%s", e.YoutubeID),
		)
		if err := cmd.Run(); err != nil {
			fmt.Printf("FAILED (%v)\n", err)
			continue
		}
		fmt.Println("OK")
	}
}

func refreshMetadata(cfg *config.Config) {
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)
	fetch := fetcher.NewService(cfg.Fetcher, cat, nil)

	entries, err := cat.GetAll()
	if err != nil {
		log.Fatalf("Failed to list: %v", err)
	}

	// Collect YouTube IDs
	var youtubeIDs []string
	for _, e := range entries {
		youtubeIDs = append(youtubeIDs, e.YoutubeID)
		fmt.Printf("  Saved: %s (%s - %s)\n", e.YoutubeID, e.Artist, e.Title)
	}

	fmt.Printf("\nWiping tables and re-adding %d videos...\n\n", len(youtubeIDs))

	// Wipe both tables
	if _, err := database.Exec(`DELETE FROM requests`); err != nil {
		log.Fatalf("Failed to clear requests: %v", err)
	}
	if _, err := database.Exec(`DELETE FROM catalogue`); err != nil {
		log.Fatalf("Failed to clear catalogue: %v", err)
	}

	// Re-add each video with fresh metadata
	for _, ytID := range youtubeIDs {
		fmt.Printf("  %s: ", ytID)

		info, err := fetch.FetchVideoInfo(ytID)
		if err != nil {
			fmt.Printf("SKIP (yt-dlp failed: %v)\n", err)
			continue
		}

		artist, title := info.CleanTitle()

		thumbPath, _ := fetch.DownloadThumbnail(ytID, info.Thumbnail)

		entry, err := cat.Add(ytID, title, artist, int(info.Duration), thumbPath)
		if err != nil {
			fmt.Printf("FAILED: %v\n", err)
			continue
		}

		fmt.Printf("[%s] %s - %s\n", entry.Code, entry.Artist, entry.Title)
	}

	fmt.Println("\nDone.")
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func requestVideo(cfg *config.Config) {
	code := ""
	apiURL := ""
	now := false
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--code":
			if i+1 < len(os.Args) {
				code = os.Args[i+1]
				i++
			}
		case "--api-url":
			if i+1 < len(os.Args) {
				apiURL = os.Args[i+1]
				i++
			}
		case "--now":
			now = true
		}
	}
	if code == "" {
		log.Fatal("Usage: rmbctl request --code <CODE> [--now] [--api-url <URL>]")
	}

	if apiURL == "" {
		if envURL := os.Getenv("RMBD_URL"); envURL != "" {
			apiURL = envURL
		} else {
			apiURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
		}
	}

	body, err := json.Marshal(map[string]interface{}{
		"code":      code,
		"caller_id": "operator",
		"force":     now,
	})
	if err != nil {
		log.Fatalf("Failed to encode request: %v", err)
	}

	url := apiURL + "/api/queue/playnow"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("Failed to reach rmbd at %s: %v", url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		var errMsg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(respBody, &errMsg)
		if errMsg.Error == "" {
			errMsg.Error = string(respBody)
		}
		log.Fatalf("Request rejected (%d): %s", resp.StatusCode, errMsg.Error)
	}

	var result struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Forced bool   `json:"forced"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		log.Fatalf("Failed to parse response: %v", err)
	}

	verb := "queued (next up)"
	if result.Forced {
		verb = "playing now"
	}
	fmt.Printf("[%s] %s - %s — %s\n", code, result.Artist, result.Title, verb)
}
