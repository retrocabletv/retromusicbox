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
	TransitionSeconds        int `yaml:"transition_seconds"`
	FillerRandomDelayMinutes int `yaml:"filler_random_delay_minutes"`
}

type FetcherConfig struct {
	YtDlpPath         string `yaml:"yt_dlp_path"`
	MaxResolution      int    `yaml:"max_resolution"`
	CacheDir           string `yaml:"cache_dir"`
	ReadyDir           string `yaml:"ready_dir"`
	ThumbnailDir       string `yaml:"thumbnail_dir"`
	MaxCacheGB         int    `yaml:"max_cache_gb"`
	PrefetchThreshold  int    `yaml:"prefetch_threshold"`
}

type QueueConfig struct {
	MaxRequestsPerCallerPerHour int    `yaml:"max_requests_per_caller_per_hour"`
	AllowDuplicateInQueue       bool   `yaml:"allow_duplicate_in_queue"`
	EmptyQueueAction            string `yaml:"empty_queue_action"`
}

type IVRConfig struct {
	Enabled              bool   `yaml:"enabled"`
	WebhookBasePath      string `yaml:"webhook_base_path"`
	WelcomeJingle        string `yaml:"welcome_jingle"`
	MaxAttempts          int    `yaml:"max_attempts"`
	GatherTimeoutSeconds int    `yaml:"gather_timeout_seconds"`
	PhoneNumber          string `yaml:"phone_number"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type ChannelConfig struct {
	Width              int    `yaml:"width"`
	Height             int    `yaml:"height"`
	PhoneNumberDisplay string `yaml:"phone_number_display"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{Port: 8080},
		Playout: PlayoutConfig{
			TransitionSeconds:        3,
			FillerRandomDelayMinutes: 5,
		},
		Fetcher: FetcherConfig{
			YtDlpPath:        "/usr/local/bin/yt-dlp",
			MaxResolution:     1080,
			CacheDir:          "data/cache",
			ReadyDir:          "data/ready",
			ThumbnailDir:      "data/thumbnails",
			MaxCacheGB:        50,
			PrefetchThreshold: 3,
		},
		Queue: QueueConfig{
			MaxRequestsPerCallerPerHour: 3,
			EmptyQueueAction:            "random_play",
		},
		IVR: IVRConfig{
			MaxAttempts:          3,
			GatherTimeoutSeconds: 10,
		},
		Database: DatabaseConfig{Path: "data/box.db"},
		Channel: ChannelConfig{
			Width:              1280,
			Height:             720,
			PhoneNumberDisplay: "0900 THE BOX",
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
