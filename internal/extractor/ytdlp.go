package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// YtDlpPath is the binary to invoke. main() overrides it from config.
var YtDlpPath = "yt-dlp"

// CookiesPath is where the Netscape-format cookies file lives. The path is
// fixed at startup (main() sets it); the file itself is created, replaced,
// or deleted at runtime via the settings UI, so every use checks existence
// first. When the file exists it is passed to yt-dlp as --cookies and used
// by the native Threads extractor.
var CookiesPath = ""

// HasCookies reports whether a cookies file is currently saved.
func HasCookies() bool {
	if CookiesPath == "" {
		return false
	}
	_, err := os.Stat(CookiesPath)
	return err == nil
}

// YtDlpJSON is the subset of yt-dlp's --dump-json output we care about.
type YtDlpJSON struct {
	Title     string        `json:"title"`
	Duration  float64       `json:"duration"`
	Thumbnail string        `json:"thumbnail"`
	Formats   []YtDlpFormat `json:"formats"`
	// Top-level URL/ext appear when yt-dlp already resolved a single format.
	URL string `json:"url"`
	Ext string `json:"ext"`
}

// YtDlpFormat mirrors one entry of the "formats" array.
type YtDlpFormat struct {
	FormatID   string  `json:"format_id"`
	URL        string  `json:"url"`
	Ext        string  `json:"ext"`
	Height     int     `json:"height"`
	Width      int     `json:"width"`
	VCodec     string  `json:"vcodec"`
	ACodec     string  `json:"acodec"`
	Filesize   int64   `json:"filesize"`
	FilesizeA  int64   `json:"filesize_approx"`
	TBR        float64 `json:"tbr"`
	FormatNote string  `json:"format_note"`
	Protocol   string  `json:"protocol"`
}

// RunYtDlp invokes yt-dlp with the given args and returns stdout. Stderr is
// parsed to classify common failures into the package's typed errors.
func RunYtDlp(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath(YtDlpPath)
	if err != nil {
		return nil, fmt.Errorf("%w: yt-dlp binary not found in PATH (looked for %q) — install it from https://github.com/yt-dlp/yt-dlp", ErrExtractorFailed, YtDlpPath)
	}

	if HasCookies() {
		args = append([]string{"--cookies", CookiesPath}, args...)
	}

	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: timed out", ErrExtractorFailed)
		}
		msg := stderr.String()
		slog.Debug("yt-dlp failed", "args", args, "stderr", msg)
		return nil, classifyStderr(msg)
	}
	return stdout.Bytes(), nil
}

// ExtractJSON runs yt-dlp with --dump-json plus any extra args against the
// URL and unmarshals the result. Shared by all site extractors.
func ExtractJSON(ctx context.Context, rawURL string, extraArgs ...string) (*YtDlpJSON, error) {
	args := append([]string{"--dump-json", "--no-playlist", "--no-warnings"}, extraArgs...)
	args = append(args, "--", rawURL)
	out, err := RunYtDlp(ctx, args...)
	if err != nil {
		return nil, err
	}
	var info YtDlpJSON
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("%w: could not parse yt-dlp output: %v", ErrExtractorFailed, err)
	}
	return &info, nil
}

func classifyStderr(msg string) error {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "private"),
		strings.Contains(lower, "login required"),
		strings.Contains(lower, "log in"),
		strings.Contains(lower, "logged in"),
		strings.Contains(lower, "--cookies"),
		strings.Contains(lower, "sign in"),
		strings.Contains(lower, "age-restricted"),
		strings.Contains(lower, "rate-limit"):
		return fmt.Errorf("%w", ErrPrivate)
	case strings.Contains(lower, "404"),
		strings.Contains(lower, "not found"),
		strings.Contains(lower, "unavailable"),
		strings.Contains(lower, "does not exist"),
		strings.Contains(lower, "no video"):
		return fmt.Errorf("%w", ErrNotFound)
	case strings.Contains(lower, "unsupported url"):
		return fmt.Errorf("%w", ErrUnsupported)
	default:
		firstLine := msg
		if i := strings.IndexByte(firstLine, '\n'); i > 0 {
			firstLine = firstLine[:i]
		}
		return fmt.Errorf("%w: %s", ErrExtractorFailed, strings.TrimSpace(firstLine))
	}
}

// BestFormats picks the most useful formats from a yt-dlp result. Streams
// with both video and audio come first, sorted by resolution descending and
// deduplicated by quality label. Sites like YouTube only serve combined
// streams at low resolution, so higher-resolution video-only streams are
// appended after them, labeled so the user knows they have no audio. Falls
// back to the top-level URL so something is always returned when one exists.
func BestFormats(info *YtDlpJSON) []Format {
	var progressive, videoOnly []YtDlpFormat
	for _, f := range info.Formats {
		// vcodec "none" is explicitly audio-only; null/"" means unknown
		// (e.g. Twitter's direct mp4s), which we keep.
		if f.URL == "" || f.VCodec == "none" {
			continue
		}
		// m3u8/dash manifest URLs are not directly downloadable by a browser.
		if strings.Contains(f.Protocol, "m3u8") || strings.Contains(f.Protocol, "dash") {
			continue
		}
		// acodec "none" is explicitly silent; unknown counts as having audio.
		if f.ACodec != "none" {
			progressive = append(progressive, f)
		} else {
			videoOnly = append(videoOnly, f)
		}
	}

	out := pickTop(progressive, 4, false)

	// Offer higher resolutions than the best combined stream. These carry
	// no audio track; the handler turns them into server-merged downloads.
	maxProgressive := 0
	for _, f := range progressive {
		if f.Height > maxProgressive {
			maxProgressive = f.Height
		}
	}
	var higher []YtDlpFormat
	for _, f := range videoOnly {
		if f.Height > maxProgressive {
			higher = append(higher, f)
		}
	}
	out = append(out, pickTop(higher, 6, true)...)

	if len(out) == 0 && info.URL != "" {
		out = append(out, Format{
			Quality:  "best",
			URL:      info.URL,
			MimeType: mimeForExt(info.Ext),
		})
	}
	return out
}

// pickTop sorts by resolution descending, dedupes by quality label, and
// returns at most limit formats.
func pickTop(pool []YtDlpFormat, limit int, videoOnly bool) []Format {
	sort.SliceStable(pool, func(i, j int) bool {
		if pool[i].Height != pool[j].Height {
			return pool[i].Height > pool[j].Height
		}
		return pool[i].TBR > pool[j].TBR
	})

	seen := make(map[string]bool)
	var out []Format
	for _, f := range pool {
		q := qualityLabel(f)
		if seen[q] {
			continue
		}
		seen[q] = true
		out = append(out, Format{
			Quality:   q,
			URL:       f.URL,
			MimeType:  mimeForExt(f.Ext),
			SizeHint:  sizeHint(f),
			VideoOnly: videoOnly,
			Height:    f.Height,
		})
		if len(out) == limit {
			break
		}
	}
	return out
}

func qualityLabel(f YtDlpFormat) string {
	if f.Height > 0 {
		return fmt.Sprintf("%dp", f.Height)
	}
	if f.FormatNote != "" {
		return f.FormatNote
	}
	return f.FormatID
}

func mimeForExt(ext string) string {
	switch ext {
	case "mp4", "m4v":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "mov":
		return "video/quicktime"
	case "":
		return "video/mp4"
	default:
		return "video/" + ext
	}
}

func sizeHint(f YtDlpFormat) string {
	size := f.Filesize
	if size == 0 {
		size = f.FilesizeA
	}
	if size == 0 {
		return ""
	}
	const mb = 1 << 20
	if size >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(size)/float64(1<<30))
	}
	return fmt.Sprintf("%.0f MB", float64(size)/float64(mb))
}

// DownloadMerged downloads the best video up to maxHeight plus the best
// audio, merges them into an mp4 in dir (requires ffmpeg), and returns the
// path of the finished file. Used for sites like YouTube that only serve
// high resolutions as separate video and audio streams.
func DownloadMerged(ctx context.Context, rawURL string, maxHeight int, dir string) (string, error) {
	// Prefer H.264 video and AAC audio so the resulting mp4 plays
	// everywhere; fall back to whatever is available.
	sel := fmt.Sprintf(
		"bestvideo[height<=%d][vcodec^=avc1]+bestaudio[ext=m4a]/bestvideo[height<=%d][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=%d]+bestaudio/best[height<=%d]/best",
		maxHeight, maxHeight, maxHeight, maxHeight)
	out, err := RunYtDlp(ctx,
		"--no-playlist", "--no-warnings",
		"-f", sel,
		"--merge-output-format", "mp4",
		"-o", dir+"/%(title).100B [%(id)s].%(ext)s",
		"--no-simulate", "--print", "after_move:filepath",
		"--", rawURL,
	)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	path := strings.TrimSpace(lines[len(lines)-1])
	if path == "" {
		return "", fmt.Errorf("%w: yt-dlp did not report an output file", ErrExtractorFailed)
	}
	return path, nil
}

// FormatDuration renders seconds as H:MM:SS or M:SS.
func FormatDuration(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	s := int(seconds)
	h, m, sec := s/3600, (s%3600)/60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// hostMatches reports whether the URL's host is the given domain or a
// subdomain of it. Extractors use it for Accepts().
func hostMatches(rawURL string, domains ...string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
