// Package handler holds the HTTP handlers for the Lasta UI and API.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/local/lasta/internal/extractor"
)

// Handler serves the pages and the /extract endpoint. The semaphore caps
// concurrent yt-dlp subprocesses.
type Handler struct {
	tmpl            *template.Template
	timeout         time.Duration
	downloadTimeout time.Duration
	sem             chan struct{}
}

func New(tmpl *template.Template, timeout, downloadTimeout time.Duration, maxConcurrent int) *Handler {
	return &Handler{
		tmpl:            tmpl,
		timeout:         timeout,
		downloadTimeout: downloadTimeout,
		sem:             make(chan struct{}, maxConcurrent),
	}
}

// indexData feeds templates/index.html.
type indexData struct {
	Cookies     cookieSitesData
	CookiesPath string
	// PrefillURL fills the input and auto-starts extraction when the page
	// was reached via a deep link (/<video-url>).
	PrefillURL string
}

// cookieSitesData feeds templates/cookies.html (the per-site slots partial).
type cookieSitesData struct {
	Sites []cookieSiteStatus
}

type cookieSiteStatus struct {
	Key   string
	Label string
	Set   bool
	Count int
	Saved string
	Msg   string
	IsErr bool
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	h.render(w, "index.html", indexData{
		Cookies:     cookieSiteStatuses("", "", false),
		CookiesPath: extractor.CookiesPath,
	})
}

// DeepLink handles paths like /threads.com/@user/post/X or
// /https://youtube.com/watch?v=Y — the video URL appended straight after
// the base URL. If a registered extractor accepts it, the index page is
// served with the URL prefilled and extraction starting immediately;
// anything else is a plain 404.
func (h *Handler) DeepLink(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimPrefix(r.URL.Path, "/")
	if r.URL.RawQuery != "" {
		raw += "?" + r.URL.RawQuery
	}
	raw = normalizeDeepLink(raw)
	if r.Method != http.MethodGet || extractor.Find(raw) == nil {
		http.NotFound(w, r)
		return
	}
	h.render(w, "index.html", indexData{
		Cookies:     cookieSiteStatuses("", "", false),
		CookiesPath: extractor.CookiesPath,
		PrefillURL:  raw,
	})
}

// normalizeDeepLink repairs the scheme: browsers and proxies often collapse
// "https://" in a path to "https:/", and pasting without a scheme should
// work too.
func normalizeDeepLink(raw string) string {
	for _, scheme := range []string{"https", "http"} {
		if rest, ok := strings.CutPrefix(raw, scheme+":/"); ok {
			return scheme + "://" + strings.TrimPrefix(rest, "/")
		}
	}
	return "https://" + raw
}

// cookieSiteStatuses builds the per-slot view, attaching msg to the slot
// identified by msgKey.
func cookieSiteStatuses(msgKey, msg string, isErr bool) cookieSitesData {
	var data cookieSitesData
	for _, site := range extractor.CookieSites {
		s := cookieSiteStatus{Key: site.Key, Label: site.Label}
		if n, saved, ok := extractor.SiteCookieInfo(site.Key); ok {
			s.Set, s.Count = true, n
			s.Saved = saved.Format("Jan 2, 15:04")
		}
		if site.Key == msgKey {
			s.Msg, s.IsErr = msg, isErr
		}
		data.Sites = append(data.Sites, s)
	}
	return data
}

// Cookies saves or clears one site's cookie slot. The cookie values are
// write-only: they are never rendered back to the page.
func (h *Handler) Cookies(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	site := extractor.CookieSiteByKey(r.FormValue("site"))
	if site == nil {
		http.Error(w, "unknown site", http.StatusBadRequest)
		return
	}

	if r.FormValue("clear") != "" {
		if err := extractor.ClearSiteCookies(site); err != nil {
			slog.Error("clearing cookies", "site", site.Key, "err", err)
			h.render(w, "cookies.html", cookieSiteStatuses(site.Key, "Could not clear cookies.", true))
			return
		}
		slog.Info("cookies cleared", "site", site.Key)
		h.render(w, "cookies.html", cookieSiteStatuses(site.Key, "Cleared.", false))
		return
	}

	content := r.FormValue("cookies")
	if strings.TrimSpace(content) == "" {
		h.render(w, "cookies.html", cookieSiteStatuses(site.Key, "Paste the contents of a cookies.txt file first.", true))
		return
	}
	n, err := extractor.SaveSiteCookies(site, content)
	if err != nil {
		slog.Error("saving cookies", "site", site.Key, "err", err)
		h.render(w, "cookies.html", cookieSiteStatuses(site.Key, err.Error(), true))
		return
	}
	slog.Info("cookies saved", "site", site.Key, "entries", n)
	h.render(w, "cookies.html", cookieSiteStatuses(site.Key, "Saved.", false))
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// resultsData feeds templates/results.html for both success and error states.
type resultsData struct {
	Error string
	Site  string
	Info  *extractor.VideoInfo
}

func (h *Handler) Extract(w http.ResponseWriter, r *http.Request) {
	rawURL := strings.TrimSpace(r.FormValue("url"))
	if rawURL == "" {
		h.respondError(w, r, http.StatusBadRequest, "Please paste a video URL.")
		return
	}

	if !h.acquireSem(w, r) {
		return
	}
	defer func() { <-h.sem }()

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	site, info, err := extractor.Extract(ctx, rawURL)
	if err != nil {
		slog.Error("extraction failed", "url", rawURL, "site", site, "err", err)
		status, msg := userFacing(err)
		h.respondError(w, r, status, msg)
		return
	}
	info.Formats = selectFormats(rawURL, info.Formats)
	if len(info.Formats) == 0 {
		h.respondError(w, r, http.StatusUnprocessableEntity, "No downloadable video formats were found for this URL.")
		return
	}

	slog.Info("extraction ok", "url", rawURL, "site", site, "formats", len(info.Formats))

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"site": site, "video": info})
		return
	}
	h.render(w, "results.html", resultsData{Site: site, Info: info})
}

// acquireSem takes a semaphore slot, giving up if the client goes away or
// the queue stays full for the whole request timeout. Returns false if the
// caller should bail (the error response is already written). On true, the
// caller must release with <-h.sem.
func (h *Handler) acquireSem(w http.ResponseWriter, r *http.Request) bool {
	select {
	case h.sem <- struct{}{}:
		return true
	case <-r.Context().Done():
		return false
	case <-time.After(h.timeout):
		h.respondError(w, r, http.StatusServiceUnavailable, "Server is busy, try again in a moment.")
		return false
	}
}

// selectFormats narrows the extractor's format list to 1080p and/or 720p,
// always with audio. Streams that already have audio keep their direct CDN
// link; video-only streams (YouTube above 360p) become server-merged
// downloads via /download. Videos with neither resolution fall back to the
// single best available rendition so something is always offered.
func selectFormats(rawURL string, formats []extractor.Format) []extractor.Format {
	var out []extractor.Format
	for _, target := range []int{1080, 720} {
		if f, ok := pickAt(rawURL, formats, target); ok {
			out = append(out, f)
		}
	}
	if len(out) > 0 {
		return out
	}

	// Neither resolution exists — offer the highest available instead.
	best := -1
	for i, f := range formats {
		if best == -1 || f.Height > formats[best].Height ||
			(f.Height == formats[best].Height && formats[best].VideoOnly && !f.VideoOnly) {
			best = i
		}
	}
	if best == -1 {
		return nil
	}
	return []extractor.Format{deliverable(rawURL, formats[best])}
}

// pickAt finds a format at exactly the target height, preferring streams
// that already include audio.
func pickAt(rawURL string, formats []extractor.Format, target int) (extractor.Format, bool) {
	idx := -1
	for i, f := range formats {
		if f.Height != target {
			continue
		}
		if !f.VideoOnly {
			return deliverable(rawURL, f), true
		}
		if idx == -1 {
			idx = i
		}
	}
	if idx == -1 {
		return extractor.Format{}, false
	}
	return deliverable(rawURL, formats[idx]), true
}

// deliverable turns a format into a server-side /download link. Every
// download is fetched by the server and streamed to the browser: CDN URLs
// (especially YouTube's) are often locked to the IP that extracted them, so
// handing them to the browser breaks as soon as Lasta runs on a different
// machine than the user.
func deliverable(rawURL string, f extractor.Format) extractor.Format {
	quality := f.Quality
	height := f.Height
	if height > 0 {
		quality = fmt.Sprintf("%dp", height)
	} else {
		height = 1080
	}
	out := extractor.Format{
		Quality:  quality,
		URL:      "/download?u=" + url.QueryEscape(rawURL) + "&h=" + strconv.Itoa(height),
		MimeType: "video/mp4",
		Height:   f.Height,
	}
	if f.SizeHint != "" {
		out.SizeHint = "~" + f.SizeHint
	}
	return out
}

// Download fetches the requested video server-side via yt-dlp, merging
// video and audio streams with ffmpeg, and streams the finished mp4 to the
// client. Used for formats that have no directly downloadable URL with
// audio.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("u")
	e := extractor.Find(rawURL)
	if e == nil {
		http.Error(w, "unsupported or missing url", http.StatusUnprocessableEntity)
		return
	}
	maxHeight, err := strconv.Atoi(r.URL.Query().Get("h"))
	if err != nil || maxHeight < 144 || maxHeight > 4320 {
		maxHeight = 1080
	}

	if !h.acquireSem(w, r) {
		return
	}
	defer func() { <-h.sem }()

	ctx, cancel := context.WithTimeout(r.Context(), h.downloadTimeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "lasta-*")
	if err != nil {
		slog.Error("temp dir", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)

	// Sites yt-dlp can't handle (Threads) download natively; the rest go
	// through yt-dlp, merging video+audio when needed.
	var path string
	if d, ok := e.(extractor.Downloader); ok {
		path, err = d.Download(ctx, rawURL, maxHeight, dir)
	} else {
		path, err = extractor.DownloadMerged(ctx, rawURL, maxHeight, dir)
	}
	if err != nil {
		slog.Error("download failed", "url", rawURL, "site", e.Name(), "err", err)
		status, msg := userFacing(err)
		http.Error(w, msg, status)
		return
	}

	name := filepath.Base(path)
	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype == "" {
		ctype = "video/mp4"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeFile(w, r, path)
}

// userFacing maps typed extractor errors to a status code and a message safe
// to show in the UI.
func userFacing(err error) (int, string) {
	switch {
	case errors.Is(err, extractor.ErrUnsupported):
		return http.StatusUnprocessableEntity, "This site isn't supported. Lasta handles YouTube, Instagram, Twitter/X, and Threads links."
	case errors.Is(err, extractor.ErrPrivate):
		return http.StatusForbidden, "This video requires a login (private account, story, or age-restricted). To access logged-in content, set YTDLP_COOKIES to an exported cookies file and restart Lasta."
	case errors.Is(err, extractor.ErrNotFound):
		return http.StatusNotFound, "Video not found. It may have been deleted, or the link is wrong."
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "Extraction timed out. The site may be slow — try again."
	default:
		return http.StatusBadGateway, "Extraction failed. Check the link, or try again later."
	}
}

func (h *Handler) respondError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if wantsJSON(r) {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	// HTMX swaps the body of error responses only when told to; returning
	// 200 with the error partial keeps the UX simple.
	h.render(w, "results.html", resultsData{Error: msg})
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("template render failed", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
