# Lasta

Self-hosted video downloader. Paste a link from YouTube, Instagram, Twitter/X, or Threads and download the video in the best available quality (1080p/720p where offered).

All downloads go through the server: it fetches the video to a temp file — via yt-dlp (merging YouTube's separate video and audio streams with ffmpeg when needed) or natively for Threads — then streams the finished mp4 to your browser and deletes the temp file. This is deliberate: many CDN URLs (especially YouTube's) are locked to the IP that extracted them, so direct links would break the moment Lasta runs on a VPS instead of your own machine.

Built in Go around [yt-dlp](https://github.com/yt-dlp/yt-dlp), with a plugin-style extractor architecture so new sites are a single small file.

## Prerequisites

- Go 1.22+
- `yt-dlp` on your `PATH` (or point `YTDLP_PATH` at the binary). Keep it current — YouTube rotates its URL signing and stale yt-dlp versions produce links that 403.
- `ffmpeg` on your `PATH`, used to merge video and audio for high-resolution YouTube downloads

## Running locally

```sh
go run ./cmd/lasta
# open http://localhost:8080
```

The binary is fully self-contained (templates and CSS are embedded), so you can also:

```sh
go build -o lasta ./cmd/lasta
./lasta
```

### Configuration

| Env var | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `YTDLP_PATH` | `yt-dlp` | yt-dlp binary, resolved via `PATH` |
| `YTDLP_COOKIES` | `<user-config-dir>/lasta/cookies.txt` | Where the cookies file is stored. Usually you don't set this — manage cookies from the web UI instead (see below) |
| `REQUEST_TIMEOUT_SECONDS` | `30` | Per-extraction timeout |
| `DOWNLOAD_TIMEOUT_SECONDS` | `600` | Timeout for server-merged downloads |
| `MAX_CONCURRENT_EXTRACTIONS` | `5` | Cap on simultaneous yt-dlp subprocesses |

### Cookies (stories, private & age-restricted videos)

Some content needs a logged-in session: Instagram stories, private accounts, age-restricted videos, login-walled Threads posts. The **Cookies** section at the bottom of the page has one slot per site (YouTube, Instagram, Twitter/X, Threads), each showing whether cookies are saved, how many, and when. Paste the contents of a Netscape-format `cookies.txt` into a site's slot (export from your browser with a "Get cookies.txt" extension); re-paste any time to refresh an expired login, or clear one site without touching the others. Pasted content is filtered to the slot's domains, so an all-sites export pasted into a slot keeps only that site's cookies — and pasting into the wrong slot fails with a clear message.

Slots are stored server-side with `0600` permissions (one file per site plus a merged `cookies.txt` that yt-dlp consumes), never rendered back to the browser, and sent by the native Threads extractor for `threads.com`/`threads.net`. If saved Threads cookies are expired or invalid, public posts still work — the extractor retries anonymously. Treat them as your login session: meant for personal, self-hosted use.

### Docker

```sh
docker build -t lasta .
docker run -p 8080:8080 lasta
```

## API

`POST /extract` with form field `url`. Returns the HTML results partial by default (HTMX-friendly), or JSON when the request has `Accept: application/json`:

```sh
curl -s -X POST -H 'Accept: application/json' \
  -d 'url=https://www.youtube.com/watch?v=dQw4w9WgXcQ' \
  http://localhost:8080/extract
```

Format URLs in the response are server-relative `/download` paths (`/download?u=<video-url>&h=<max-height>`); `GET` one to receive the file. `GET /health` returns `200 OK`.

**Deep links**: append a video URL straight after the base URL and extraction starts immediately on page load — with or without the scheme:

```
http://localhost:8080/threads.com/@user/post/XXXX
http://localhost:8080/www.youtube.com/watch?v=YYYY
http://localhost:8080/https://x.com/user/status/123
```

## Adding a new extractor

1. **Create `internal/extractor/<site>.go`** with a type implementing the `Extractor` interface (`internal/extractor/extractor.go`): `Name()`, `Accepts(rawURL)` (use the `hostMatches` helper to match domains), and `Extract(ctx, rawURL)`.
2. **Call the shared yt-dlp helpers** — `ExtractJSON(ctx, rawURL, extraArgs...)` runs yt-dlp with any site-specific flags, and `BestFormats(info)` picks and sorts the downloadable formats. Wrap failures in the typed errors (`ErrPrivate`, `ErrNotFound`, `ErrExtractorFailed`); `RunYtDlp` already classifies common yt-dlp stderr patterns for you.
3. **Register it** by appending the type to the `Registry` slice in `internal/extractor/registry.go`. The handler picks the first extractor whose `Accepts()` matches — no other code changes needed.

## Layout

```
cmd/lasta/main.go        entry point, wiring
assets.go                go:embed of templates/ and static/
internal/
  config/                env-var configuration
  extractor/             interface, registry, yt-dlp helper, one file per site
  handler/               HTTP handlers + chi routes
  server/                http.Server with graceful shutdown
templates/               index page + results partial (html/template)
static/                  stylesheet
```
