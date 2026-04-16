package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Playout  PlayoutConfig  `yaml:"playout"`
	Fetcher  FetcherConfig  `yaml:"fetcher"`
	Queue    QueueConfig    `yaml:"queue"`
	IVR      IVRConfig      `yaml:"ivr"`
	Database DatabaseConfig `yaml:"database"`
	Channel  ChannelConfig  `yaml:"channel"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type PlayoutConfig struct {
	TransitionSeconds        int    `yaml:"transition_seconds"`
	FillerRandomDelayMinutes int    `yaml:"filler_random_delay_minutes"`
	AdsDir                   string `yaml:"ads_dir"`
	AdsEveryNVideos          int    `yaml:"ads_every_n_videos"`
	AdMaxSeconds             int    `yaml:"ad_max_seconds"`
}

type FetcherConfig struct {
	YtDlpPath         string  `yaml:"yt_dlp_path"`
	MaxResolution      int     `yaml:"max_resolution"`
	CacheDir           string  `yaml:"cache_dir"`
	ReadyDir           string  `yaml:"ready_dir"`
	ThumbnailDir       string  `yaml:"thumbnail_dir"`
	MaxCacheGB         int     `yaml:"max_cache_gb"`
	PrefetchThreshold  int     `yaml:"prefetch_threshold"`
	// LoudnessTargetLUFS is the integrated loudness target (EBU R128) applied
	// by ffmpeg's `loudnorm` filter during transcode. 0 disables normalisation.
	// -14 LUFS matches streaming-era norms (YouTube/Spotify-ish); -16 is the
	// podcast/broadcast default.
	LoudnessTargetLUFS float64 `yaml:"loudness_target_lufs"`
}

type QueueConfig struct {
	MaxRequestsPerCallerPerHour int    `yaml:"max_requests_per_caller_per_hour"`
	AllowDuplicateInQueue       bool   `yaml:"allow_duplicate_in_queue"`
	EmptyQueueAction            string `yaml:"empty_queue_action"`
}

// IVRConfig controls the service-agnostic IVR session API. The API
// itself is fixed at /api/ivr/sessions — any voice front-end (Jambonz,
// Twilio, Asterisk, a DIY DTMF decoder) can drive it by POSTing digits
// to a session it creates.
type IVRConfig struct {
	Enabled bool `yaml:"enabled"`
	// ConfirmTTLSeconds is how long a session can sit in the `validated`
	// state waiting for the caller to press 1 (confirm) or 2 (cancel)
	// before the reaper drops it. 0 uses the built-in default (15s).
	ConfirmTTLSeconds int `yaml:"confirm_ttl_seconds"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type ChannelConfig struct {
	Width              int    `yaml:"width"`
	Height             int    `yaml:"height"`
	PhoneNumberDisplay string `yaml:"phone_number_display"`
	CRTEnabled         bool   `yaml:"crt_enabled"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{Port: 8080},
		Playout: PlayoutConfig{
			TransitionSeconds:        1,
			FillerRandomDelayMinutes: 5,
			AdsDir:                   "assets/ads",
			AdsEveryNVideos:          0,
			AdMaxSeconds:             90,
		},
		Fetcher: FetcherConfig{
			YtDlpPath:          "yt-dlp",
			MaxResolution:      1080,
			CacheDir:           "data/cache",
			ReadyDir:           "data/ready",
			ThumbnailDir:       "data/thumbnails",
			MaxCacheGB:         50,
			PrefetchThreshold:  3,
			LoudnessTargetLUFS: -14.0,
		},
		Queue: QueueConfig{
			MaxRequestsPerCallerPerHour: 3,
			EmptyQueueAction:            "random_play",
		},
		IVR: IVRConfig{Enabled: true, ConfirmTTLSeconds: 15},
		Database: DatabaseConfig{Path: "data/retromusicbox.db"},
		Channel: ChannelConfig{
			Width:              1280,
			Height:             720,
			PhoneNumberDisplay: "0900 RETROMUSICBOX",
			CRTEnabled:         true,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
