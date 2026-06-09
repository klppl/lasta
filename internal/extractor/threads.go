package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const chromeUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

// Threads extracts videos from threads.net / threads.com posts.
//
// yt-dlp has no Threads extractor, and Threads only server-side-renders post
// data when the request carries a full set of Chrome navigation headers
// (sec-ch-ua + sec-fetch + browser UA); anything less gets an empty app
// shell or a redirect to /?error=invalid_post. So this extractor fetches
// the page itself and parses the post JSON embedded in the
// `<script type="application/json" data-sjs>` blocks.
type Threads struct{}

func (Threads) Name() string { return "Threads" }

var threadsPostRe = regexp.MustCompile(`^/(?:@[^/]+/)?(?:post|t)/([A-Za-z0-9_-]+)`)

func (Threads) Accepts(rawURL string) bool {
	return hostMatches(rawURL, "threads.net", "threads.com")
}

func (Threads) Extract(ctx context.Context, rawURL string) (*VideoInfo, error) {
	code, err := threadsPostCode(rawURL)
	if err != nil {
		return nil, err
	}

	// Try with saved cookies first (needed for login-walled posts), but
	// fall back to an anonymous fetch: expired or invalid cookies would
	// otherwise break public posts too.
	attempts := []bool{false}
	if HasCookies() {
		attempts = []bool{true, false}
	}
	var lastErr error
	for _, withCookies := range attempts {
		body, err := fetchThreadsPage(ctx, rawURL, withCookies)
		if err != nil {
			lastErr = err
			continue
		}
		if node := findThreadsMedia(body, code); node != nil {
			return threadsVideoInfo(node), nil
		}
		lastErr = fmt.Errorf("%w: no video found in Threads post %s", ErrNotFound, code)
	}
	return nil, lastErr
}

// Download fetches the post's video from the CDN into dir. Threads videos
// are complete mp4 files, so no merging is needed.
func (t Threads) Download(ctx context.Context, rawURL string, maxHeight int, dir string) (string, error) {
	info, err := t.Extract(ctx, rawURL)
	if err != nil {
		return "", err
	}
	if len(info.Formats) == 0 {
		return "", fmt.Errorf("%w: no downloadable format", ErrNotFound)
	}
	src := info.Formats[0]

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrExtractorFailed, err)
	}
	req.Header.Set("User-Agent", chromeUA)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: fetching video: %v", ErrExtractorFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: CDN returned HTTP %d", ErrExtractorFailed, resp.StatusCode)
	}

	path := filepath.Join(dir, sanitizeFilename(info.Title)+".mp4")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrExtractorFailed, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("%w: downloading video: %v", ErrExtractorFailed, err)
	}
	return path, nil
}

// sanitizeFilename keeps a title usable as a filename.
func sanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\x00':
			return '_'
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 100 {
		s = s[:100]
	}
	if s == "" {
		s = "threads-video"
	}
	return s
}

func threadsPostCode(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid URL", ErrUnsupported)
	}
	m := threadsPostRe.FindStringSubmatch(u.Path)
	if m == nil {
		return "", fmt.Errorf("%w: not a Threads post URL", ErrUnsupported)
	}
	return m[1], nil
}

// fetchThreadsPage downloads the post page disguised as a Chrome navigation.
// A redirect to /?error=invalid_post means the post is login-walled or gone.
func fetchThreadsPage(ctx context.Context, rawURL string, withCookies bool) ([]byte, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if strings.Contains(req.URL.RawQuery, "error=invalid_post") ||
				strings.Contains(req.URL.Path, "/login") {
				return fmt.Errorf("post unavailable")
			}
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExtractorFailed, err)
	}
	for k, v := range map[string]string{
		"User-Agent":                chromeUA,
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
		"Accept-Language":           "en-US,en;q=0.9",
		"sec-ch-ua":                 `"Google Chrome";v="149", "Chromium";v="149", "Not?A_Brand";v="24"`,
		"sec-ch-ua-mobile":          "?0",
		"sec-ch-ua-platform":        `"Linux"`,
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-User":            "?1",
		"Sec-Fetch-Dest":            "document",
		"Upgrade-Insecure-Requests": "1",
	} {
		req.Header.Set(k, v)
	}
	if withCookies {
		if c := CookieHeaderFor("threads.net", "threads.com"); c != "" {
			req.Header.Set("Cookie", c)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "post unavailable") {
			return nil, fmt.Errorf("%w: Threads redirected to invalid_post", ErrPrivate)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: timed out", ErrExtractorFailed)
		}
		return nil, fmt.Errorf("%w: %v", ErrExtractorFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: Threads returned HTTP %d", ErrExtractorFailed, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrExtractorFailed, err)
	}
	return body, nil
}

var threadsScriptRe = regexp.MustCompile(`(?s)<script type="application/json"[^>]*data-sjs[^>]*>(.*?)</script>`)

// findThreadsMedia walks the embedded JSON blobs for the post node whose
// "code" matches the URL, then searches that post's subtree for video
// streams. The video may sit on the post itself, inside carousel_media, or
// on a quoted/reposted attachment whose code differs from the URL's.
func findThreadsMedia(body []byte, code string) map[string]any {
	for _, m := range threadsScriptRe.FindAllSubmatch(body, -1) {
		script := m[1]
		if !strings.Contains(string(script), `"video_versions"`) {
			continue
		}
		var data any
		if err := json.Unmarshal(script, &data); err != nil {
			continue
		}
		var posts []map[string]any
		collectByCode(data, code, &posts)
		for _, post := range posts {
			if hit := walkForVideo(post); hit != nil {
				return hit
			}
		}
	}
	return nil
}

// collectByCode gathers every map node whose "code" equals the post code.
func collectByCode(v any, code string, out *[]map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		if t["code"] == code {
			*out = append(*out, t)
		}
		for _, child := range t {
			collectByCode(child, code, out)
		}
	case []any:
		for _, child := range t {
			collectByCode(child, code, out)
		}
	}
}

// walkForVideo returns the first node in the subtree carrying a non-empty
// video_versions list.
func walkForVideo(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		if vv, ok := t["video_versions"].([]any); ok && len(vv) > 0 {
			return t
		}
		for _, child := range t {
			if hit := walkForVideo(child); hit != nil {
				return hit
			}
		}
	case []any:
		for _, child := range t {
			if hit := walkForVideo(child); hit != nil {
				return hit
			}
		}
	}
	return nil
}

func threadsVideoInfo(node map[string]any) *VideoInfo {
	info := &VideoInfo{Title: "Threads post"}

	if cap, ok := node["caption"].(map[string]any); ok {
		if text, ok := cap["text"].(string); ok && text != "" {
			info.Title = text
			if i := strings.IndexByte(info.Title, '\n'); i > 0 {
				info.Title = info.Title[:i]
			}
		}
	}
	if iv, ok := node["image_versions2"].(map[string]any); ok {
		if cands, ok := iv["candidates"].([]any); ok && len(cands) > 0 {
			if c, ok := cands[0].(map[string]any); ok {
				info.Thumb, _ = c["url"].(string)
			}
		}
	}
	if d, ok := node["video_duration"].(float64); ok {
		info.Duration = FormatDuration(d)
	}

	height := 0
	if h, ok := node["original_height"].(float64); ok {
		height = int(h)
	}

	// Type 101 is the primary (highest quality) rendition; keep the first
	// URL of each distinct type.
	vv, _ := node["video_versions"].([]any)
	seen := make(map[string]bool)
	for _, raw := range vv {
		v, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		u, _ := v["url"].(string)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		q := "best"
		if height > 0 {
			q = fmt.Sprintf("%dp", height)
		}
		info.Formats = append(info.Formats, Format{
			Quality:  q,
			URL:      u,
			MimeType: "video/mp4",
			Height:   height,
		})
		// One rendition is enough — the rest are lower-quality duplicates.
		break
	}
	return info
}
