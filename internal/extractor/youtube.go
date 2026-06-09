package extractor

import "context"

// YouTube extracts videos from youtube.com and youtu.be via yt-dlp.
type YouTube struct{}

func (YouTube) Name() string { return "YouTube" }

func (YouTube) Accepts(rawURL string) bool {
	return hostMatches(rawURL, "youtube.com", "youtu.be", "youtube-nocookie.com")
}

func (YouTube) Extract(ctx context.Context, rawURL string) (*VideoInfo, error) {
	// Prefer progressive mp4 so the browser can download a single file;
	// fall back to best available.
	info, err := ExtractJSON(ctx, rawURL, "-f", "best[ext=mp4]/best")
	if err != nil {
		return nil, err
	}
	return &VideoInfo{
		Title:    info.Title,
		Duration: FormatDuration(info.Duration),
		Thumb:    info.Thumbnail,
		Formats:  BestFormats(info),
	}, nil
}
