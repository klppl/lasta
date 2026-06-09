package main

import (
	"html/template"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	lasta "github.com/local/lasta"
	"github.com/local/lasta/internal/config"
	"github.com/local/lasta/internal/extractor"
	"github.com/local/lasta/internal/handler"
	"github.com/local/lasta/internal/server"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg := config.Load()
	extractor.YtDlpPath = cfg.YtDlpPath

	// Cookies live in the user config dir unless YTDLP_COOKIES points
	// elsewhere. The file is managed at runtime through the settings UI.
	if cfg.YtDlpCookies == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			cfg.YtDlpCookies = filepath.Join(dir, "lasta", "cookies.txt")
		} else {
			cfg.YtDlpCookies = "lasta-cookies.txt"
		}
	}
	extractor.CookiesPath = cfg.YtDlpCookies

	tmpl, err := template.ParseFS(lasta.Assets, "templates/*.html")
	if err != nil {
		slog.Error("parsing templates", "err", err)
		os.Exit(1)
	}
	staticFS, err := fs.Sub(lasta.Assets, "static")
	if err != nil {
		slog.Error("static fs", "err", err)
		os.Exit(1)
	}

	h := handler.New(tmpl, cfg.RequestTimeout, cfg.DownloadTimeout, cfg.MaxConcurrentExtractions)
	if err := server.Run(cfg.Port, handler.Routes(h, staticFS)); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
