package extractor

import "context"

// Twitter extracts videos from twitter.com / x.com via yt-dlp.
type Twitter struct{}

func (Twitter) Name() string { return "Twitter/X" }

func (Twitter) Accepts(rawURL string) bool {
	return hostMatches(rawURL, "twitter.com", "x.com", "t.co")
}

func (Twitter) Extract(ctx context.Context, rawURL string) (*VideoInfo, error) {
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
