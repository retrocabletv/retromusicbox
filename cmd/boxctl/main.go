package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/alexkinch/thebox/internal/catalogue"
	"github.com/alexkinch/thebox/internal/config"
	"github.com/alexkinch/thebox/internal/db"
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
	case "remove":
		removeVideo(cfg)
	case "list":
		listVideos(cfg)
	case "search":
		searchVideos(cfg)
	case "cache-all":
		cacheAll(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("boxctl - The Box catalogue manager")
	fmt.Println()
	fmt.Println("Usage: boxctl <command> [args]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  init-db                    Initialise the database")
	fmt.Println("  add --youtube <ID>         Add a video by YouTube ID")
	fmt.Println("  remove --code <CODE>       Remove a video by catalogue code")
	fmt.Println("  list                       List all catalogue entries")
	fmt.Println("  search --query <QUERY>     Search by artist/title")
	fmt.Println("  cache-all                  Fetch and transcode all catalogue videos")
}

func loadConfig() *config.Config {
	configPath := "configs/config.yaml"
	if envPath := os.Getenv("BOX_CONFIG"); envPath != "" {
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
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--youtube" && i+1 < len(os.Args) {
			ytID = os.Args[i+1]
			break
		}
	}
	if ytID == "" {
		log.Fatal("Usage: boxctl add --youtube <YOUTUBE_ID>")
	}

	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	cat := catalogue.NewService(database)

	// Check if already exists
	existing, _ := cat.GetByYoutubeID(ytID)
	if existing != nil {
		fmt.Printf("Already in catalogue: [%s] %s - %s\n", existing.Code, existing.Artist, existing.Title)
		return
	}

	// Fetch info with yt-dlp
	fmt.Printf("Fetching info for %s...\n", ytID)
	ytDlpPath := cfg.Fetcher.YtDlpPath
	cmd := exec.Command(ytDlpPath, "--dump-json", "--no-download",
		fmt.Sprintf("https://www.youtube.com/watch?v=%s", ytID))
	output, err := cmd.Output()
	if err != nil {
		log.Fatalf("yt-dlp failed: %v", err)
	}

	var info struct {
		Title    string  `json:"title"`
		Uploader string  `json:"uploader"`
		Duration float64 `json:"duration"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		log.Fatalf("Parse error: %v", err)
	}

	entry, err := cat.Add(ytID, info.Title, info.Uploader, int(info.Duration), "")
	if err != nil {
		log.Fatalf("Failed to add: %v", err)
	}

	fmt.Printf("Added: [%s] %s - %s (%ds)\n", entry.Code, entry.Artist, entry.Title, derefInt(entry.DurationSeconds))
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
		log.Fatal("Usage: boxctl remove --code <CODE>")
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
		log.Fatal("Usage: boxctl search --query <QUERY>")
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

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
