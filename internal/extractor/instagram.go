package extractor

import "context"

// Instagram extracts reels and video posts from instagram.com via yt-dlp.
type Instagram struct{}

func (Instagram) Name() string { return "Instagram" }

func (Instagram) Accepts(rawURL string) bool {
	return hostMatches(rawURL, "instagram.com")
}

func (Instagram) Extract(ctx context.Context, rawURL string) (*VideoInfo, error) {
	info, err := ExtractJSON(ctx, rawURL)
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
