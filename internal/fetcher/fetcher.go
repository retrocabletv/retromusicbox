package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/alexkinch/thebox/internal/catalogue"
	"github.com/alexkinch/thebox/internal/config"
	"github.com/alexkinch/thebox/internal/queue"
)

type VideoInfo struct {
	Title       string  `json:"title"`
	Uploader    string  `json:"uploader"`
	Duration    float64 `json:"duration"`
	Thumbnail   string  `json:"thumbnail"`
}

type Service struct {
	cfg       config.FetcherConfig
	catalogue *catalogue.Service
	queue     *queue.Service
	mu        sync.Mutex
	fetching  map[string]bool // youtube_id -> currently fetching
	onReady   func(catalogueCode string) // callback when a video becomes ready
}

func NewService(cfg config.FetcherConfig, cat *catalogue.Service, q *queue.Service) *Service {
	os.MkdirAll(cfg.CacheDir, 0755)
	os.MkdirAll(cfg.ReadyDir, 0755)
	os.MkdirAll(cfg.ThumbnailDir, 0755)

	return &Service{
		cfg:       cfg,
		catalogue: cat,
		queue:     q,
		fetching:  make(map[string]bool),
	}
}

func (s *Service) SetOnReady(fn func(string)) {
	s.onReady = fn
}

func (s *Service) UpdateYtDlp() {
	log.Println("[fetcher] updating yt-dlp...")
	cmd := exec.Command(s.cfg.YtDlpPath, "-U")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[fetcher] yt-dlp update failed: %v", err)
	}
}

func (s *Service) FetchVideoInfo(youtubeID string) (*VideoInfo, error) {
	cmd := exec.Command(s.cfg.YtDlpPath, "--dump-json", "--no-download",
		fmt.Sprintf("https://www.youtube.com/watch?v=%s", youtubeID))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp info failed: %w", err)
	}

	var info VideoInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("parse yt-dlp output: %w", err)
	}
	return &info, nil
}

func (s *Service) DownloadThumbnail(youtubeID, thumbnailURL string) (string, error) {
	if thumbnailURL == "" {
		return "", nil
	}

	thumbPath := filepath.Join(s.cfg.ThumbnailDir, youtubeID+".jpg")
	if _, err := os.Stat(thumbPath); err == nil {
		return thumbPath, nil
	}

	resp, err := http.Get(thumbnailURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.Create(thumbPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return thumbPath, nil
}

func (s *Service) FetchAndTranscode(youtubeID string) error {
	s.mu.Lock()
	if s.fetching[youtubeID] {
		s.mu.Unlock()
		return nil // already fetching
	}
	s.fetching[youtubeID] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.fetching, youtubeID)
		s.mu.Unlock()
	}()

	readyPath := filepath.Join(s.cfg.ReadyDir, youtubeID+".mp4")
	if _, err := os.Stat(readyPath); err == nil {
		return nil // already transcoded
	}

	// Download
	cachePath := filepath.Join(s.cfg.CacheDir, youtubeID+".mkv")
	if _, err := os.Stat(cachePath); err != nil {
		log.Printf("[fetcher] downloading %s", youtubeID)
		cmd := exec.Command(s.cfg.YtDlpPath,
			"-f", fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]", s.cfg.MaxResolution, s.cfg.MaxResolution),
			"--merge-output-format", "mkv",
			"-o", cachePath,
			fmt.Sprintf("https://www.youtube.com/watch?v=%s", youtubeID),
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("yt-dlp download failed: %w", err)
		}
	}

	// Transcode
	log.Printf("[fetcher] transcoding %s", youtubeID)
	tmpPath := readyPath + ".tmp"
	cmd := exec.Command("ffmpeg", "-y", "-i", cachePath,
		"-vf", "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2,setsar=1",
		"-r", "25",
		"-c:v", "libx264", "-profile:v", "high", "-level", "4.1", "-preset", "fast", "-crf", "20",
		"-movflags", "+faststart",
		"-c:a", "aac", "-ar", "48000", "-ac", "2", "-b:a", "192k",
		"-f", "mp4",
		tmpPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("ffmpeg transcode failed: %w", err)
	}

	if err := os.Rename(tmpPath, readyPath); err != nil {
		return fmt.Errorf("rename transcoded file: %w", err)
	}

	// Update catalogue
	if err := s.catalogue.SetVideoPath(youtubeID, readyPath); err != nil {
		log.Printf("[fetcher] warning: failed to update video_path: %v", err)
	}

	// Clean up raw download
	os.Remove(cachePath)

	log.Printf("[fetcher] ready: %s", youtubeID)
	return nil
}

func (s *Service) IsReady(youtubeID string) bool {
	readyPath := filepath.Join(s.cfg.ReadyDir, youtubeID+".mp4")
	_, err := os.Stat(readyPath)
	return err == nil
}

func (s *Service) ReadyPath(youtubeID string) string {
	return filepath.Join(s.cfg.ReadyDir, youtubeID+".mp4")
}

// StartPrefetchWorker watches the queue and prefetches upcoming videos.
func (s *Service) StartPrefetchWorker(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.prefetchUpcoming()
		}
	}
}

func (s *Service) prefetchUpcoming() {
	topN, err := s.queue.GetTopN(s.cfg.PrefetchThreshold)
	if err != nil {
		log.Printf("[fetcher] error getting top queue items: %v", err)
		return
	}

	for _, req := range topN {
		entry, err := s.catalogue.GetByCode(req.CatalogueCode)
		if err != nil || entry == nil {
			continue
		}

		if s.IsReady(entry.YoutubeID) {
			if req.Status == "queued" || req.Status == "fetching" {
				s.queue.SetReady(req.CatalogueCode)
			}
			continue
		}

		go func(ytID, code string, reqID int64) {
			s.queue.UpdateStatus(reqID, "fetching")
			if err := s.FetchAndTranscode(ytID); err != nil {
				log.Printf("[fetcher] failed to fetch %s: %v", ytID, err)
				s.queue.MarkFailed(reqID)
				return
			}
			s.queue.SetReady(code)
			if s.onReady != nil {
				s.onReady(code)
			}
		}(entry.YoutubeID, entry.Code, req.ID)
	}
}

// EvictCache removes least-recently-played cached videos to stay under limit.
func (s *Service) EvictCache() error {
	maxBytes := int64(s.cfg.MaxCacheGB) * 1024 * 1024 * 1024

	entries, err := filepath.Glob(filepath.Join(s.cfg.ReadyDir, "*.mp4"))
	if err != nil {
		return err
	}

	type fileInfo struct {
		path    string
		modTime time.Time
		size    int64
	}

	var files []fileInfo
	var totalSize int64
	for _, path := range entries {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path: path, modTime: info.ModTime(), size: info.Size()})
		totalSize += info.Size()
	}

	if totalSize <= maxBytes {
		return nil
	}

	// Sort oldest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	for _, f := range files {
		if totalSize <= maxBytes {
			break
		}
		log.Printf("[fetcher] evicting %s (%d MB)", f.path, f.size/1024/1024)
		os.Remove(f.path)
		totalSize -= f.size

		// Also clear the video_path in catalogue
		base := filepath.Base(f.path)
		ytID := base[:len(base)-4] // strip .mp4
		s.catalogue.SetVideoPath(ytID, "")
	}

	return nil
}
