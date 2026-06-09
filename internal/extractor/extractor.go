// Package extractor defines the shared extractor interface and the typed
// errors every site extractor returns. Each supported site lives in its own
// file in this package and registers itself in registry.go.
package extractor

import (
	"context"
	"errors"
)

// Typed errors. Handlers map these to user-facing messages; extractors must
// wrap site-specific failures into one of them.
var (
	ErrUnsupported     = errors.New("no extractor supports this URL")
	ErrPrivate         = errors.New("video is private or requires login")
	ErrNotFound        = errors.New("video not found")
	ErrExtractorFailed = errors.New("extraction failed")
)

// Format is a single downloadable rendition of a video.
type Format struct {
	Quality  string
	URL      string
	MimeType string
	SizeHint string
	// VideoOnly marks streams without an audio track (e.g. YouTube DASH
	// video above 360p). The handler offers these as a server-merged
	// download instead of a direct link.
	VideoOnly bool
	// Height is the vertical resolution, used to pick merge targets.
	Height int
}

// VideoInfo is the normalized result every extractor produces.
type VideoInfo struct {
	Title    string
	Duration string
	Thumb    string
	Formats  []Format
}

// Extractor is implemented once per supported site.
type Extractor interface {
	// Name is a short human-readable site name, e.g. "YouTube".
	Name() string
	// Accepts reports whether this extractor can handle the URL.
	Accepts(rawURL string) bool
	// Extract resolves the URL into video metadata and direct format URLs.
	Extract(ctx context.Context, rawURL string) (*VideoInfo, error)
}

// Downloader is implemented by extractors that fetch the video file
// themselves instead of going through yt-dlp (e.g. Threads, which yt-dlp
// does not support). Download writes the video into dir and returns the
// file path.
type Downloader interface {
	Download(ctx context.Context, rawURL string, maxHeight int, dir string) (string, error)
}
