# --- Build stage ---
FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lasta ./cmd/lasta

# --- Runtime stage ---
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl python3 ffmpeg \
    && curl -fsSL -o /usr/local/bin/yt-dlp \
        https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp \
    && chmod a+rx /usr/local/bin/yt-dlp \
    && apt-get purge -y curl \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /data && chown nobody:nogroup /data

COPY --from=build /out/lasta /usr/local/bin/lasta

# Cookies are saved under $XDG_CONFIG_HOME/lasta; mount /data to persist
# them across container restarts. HOME also points there so yt-dlp has a
# writable cache dir.
ENV PORT=8080 HOME=/data XDG_CONFIG_HOME=/data
VOLUME /data
EXPOSE 8080

USER nobody
ENTRYPOINT ["/usr/local/bin/lasta"]
