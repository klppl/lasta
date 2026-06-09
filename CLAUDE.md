# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
go build ./...                 # build everything
go run ./cmd/lasta             # run locally on :8080 (needs yt-dlp + ffmpeg on PATH)
go vet ./...                   # lint
docker build -t lasta .        # self-contained image (bundles yt-dlp + ffmpeg)
```

There are no tests yet (`go test ./...` finds none). Manual end-to-end checks are done with curl against a running server; `POST /extract` with `Accept: application/json` returns JSON instead of the HTML partial:

```sh
curl -s -X POST -H 'Accept: application/json' --data-urlencode 'url=<video-url>' localhost:8080/extract
```

External runtime dependencies: **yt-dlp must be current** — YouTube rotates URL signing and a stale yt-dlp produces 403 links or sees only one throttled format (this presented as "download links don't work"). ffmpeg is required for merged downloads. The host has both; the Docker image downloads the latest yt-dlp at build time, so rebuilding the image is the yt-dlp upgrade path.

## Architecture

Module `github.com/local/lasta`. Single static binary; templates/ and static/ are embedded via `assets.go` at the repo root — go:embed paths are file-relative, which is why the embed cannot live in `cmd/lasta/`.

Request flow: `cmd/lasta/main.go` wires config → `internal/handler` (chi routes) → `internal/extractor` registry. The handler loops `extractor.Registry` for the first `Accepts(url)` match. Extractors return typed errors (`ErrUnsupported`, `ErrPrivate`, `ErrNotFound`, `ErrExtractorFailed`); `handler.userFacing()` maps them to UI messages — extractors must wrap failures in these, never leak raw errors to templates.

**All downloads are proxied through the server** (`/download?u=<url>&h=<height>`). This is deliberate: YouTube CDN URLs are IP-locked to the extracting machine, so direct links break when Lasta runs on a VPS. The server downloads to a temp dir (deleted after serving) and streams the file. Two download paths in `handler.Download`:
- Default: `extractor.DownloadMerged` — yt-dlp with a format selector preferring H.264+AAC (AV1 breaks older players), ffmpeg-merges video+audio into mp4. Needed because YouTube serves combined video+audio only at 360p.
- Extractors implementing the optional `Downloader` interface fetch natively (Threads does, since yt-dlp can't).

**Format selection** happens in two stages: `extractor.BestFormats` (ytdlp.go) filters yt-dlp's format list — keep only browser-downloadable formats, where vcodec `"none"` means audio-only but `null`/`""` means *unknown* and must be kept (Twitter's direct mp4s report null codecs; treating unknown as "no video" broke Twitter entirely). Then `handler.selectFormats` narrows to exactly 1080p and/or 720p with a best-available fallback (product decision: show nothing else).

**Threads is fully native** (`internal/extractor/threads.go`) — yt-dlp has no Threads extractor. It fetches the post page with a complete Chrome navigation header set (`sec-ch-ua` + `Sec-Fetch-*` + UA together; anything less gets an empty app shell or a redirect to `/?error=invalid_post`, which means login-walled/deleted → `ErrPrivate`). Post data is parsed from `<script type="application/json" data-sjs>` blobs: find nodes whose `code` matches the URL's post code, then search that subtree for `video_versions` — the video may sit on the post itself, in `carousel_media`, or on a quoted/reposted attachment whose code differs from the URL's. Meta rotates anti-bot measures; expect this extractor to need maintenance.

**Cookies** (`internal/extractor/cookies.go`): per-site slots in the UI (`POST /cookies`), one Netscape-format file per site under the config dir, filtered to each site's domains on save (YouTube includes google.com — auth cookies live there). Every save/clear regenerates the merged `cookies.txt` that yt-dlp consumes via `--cookies`; the Threads extractor reads the merged file through `CookieHeaderFor` and **falls back to an anonymous fetch if the cookied request fails** (expired cookies must not break public posts). Cookie values are write-only — never rendered back to any template.

Concurrency: one semaphore channel in `Handler` caps simultaneous yt-dlp/download subprocesses (`MAX_CONCURRENT_EXTRACTIONS`). Extractions use `REQUEST_TIMEOUT_SECONDS`, downloads the longer `DOWNLOAD_TIMEOUT_SECONDS`, both via `context.WithTimeout` so subprocesses die with the request.

Frontend is HTMX: `POST /extract` returns the `results.html` partial swapped into `#results` (errors render as the same partial with `.Error` set, HTTP 200). Deep links: the chi `NotFound` handler treats any unmatched GET path as a video URL (`/threads.com/@user/post/X`, scheme optional, collapsed `https:/` repaired) and serves the index with the form prefilled and `hx-trigger="load"` auto-submitting.

Adding a site: new file in `internal/extractor/` implementing `Extractor` (use `hostMatches` for `Accepts`, `ExtractJSON`+`BestFormats` if yt-dlp supports the site), append to `Registry` in registry.go. Nothing else changes.

## Deployment notes

Runs as `nobody` in Docker with `HOME=/data` / `XDG_CONFIG_HOME=/data` (volume) — cookies break without a writable config dir. `.github/workflows/docker.yml` is a manual workflow pushing to ghcr.io. The app has no auth; deployments are expected to sit behind a reverse proxy with basic auth or a VPN.
