// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                     string
	YtDlpPath                string
	YtDlpCookies             string
	RequestTimeout           time.Duration
	DownloadTimeout          time.Duration
	MaxConcurrentExtractions int
}

func Load() Config {
	return Config{
		Port:                     getenv("PORT", "8080"),
		YtDlpPath:                getenv("YTDLP_PATH", "yt-dlp"),
		YtDlpCookies:             os.Getenv("YTDLP_COOKIES"),
		RequestTimeout:           time.Duration(getenvInt("REQUEST_TIMEOUT_SECONDS", 30)) * time.Second,
		DownloadTimeout:          time.Duration(getenvInt("DOWNLOAD_TIMEOUT_SECONDS", 600)) * time.Second,
		MaxConcurrentExtractions: getenvInt("MAX_CONCURRENT_EXTRACTIONS", 5),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
