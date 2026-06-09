package extractor

import "context"

// YouTube extracts videos from youtube.com and youtu.be via yt-dlp.
type YouTube struct{}

func (YouTube) Name() string { return "YouTube" }

func (YouTube) Accepts(rawURL string) bool {
	return hostMatches(rawURL, "youtube.com", "youtu.be", "youtube-nocookie.com")
}

func (YouTube) Extract(ctx context.Context, rawURL string) (*VideoInfo, error) {
	// No -f selector here: "best" means a combined video+audio file, which
	// YouTube no longer offers for many videos (especially on logged-in
	// sessions), and a non-matching selector makes yt-dlp abort instead of
	// listing formats. We only need the formats array; the actual download
	// runs through DownloadMerged with its own fallback chain.
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
