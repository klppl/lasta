package extractor

import "context"

// Registry lists all available extractors. To add a site, implement
// Extractor in a new file and append it here.
var Registry = []Extractor{
	YouTube{},
	Instagram{},
	Twitter{},
	Threads{},
}

// Find returns the first extractor that accepts the URL, or nil.
func Find(rawURL string) Extractor {
	for _, e := range Registry {
		if e.Accepts(rawURL) {
			return e
		}
	}
	return nil
}

// Extract dispatches to the matching extractor, returning ErrUnsupported
// when no extractor accepts the URL.
func Extract(ctx context.Context, rawURL string) (string, *VideoInfo, error) {
	e := Find(rawURL)
	if e == nil {
		return "", nil, ErrUnsupported
	}
	info, err := e.Extract(ctx, rawURL)
	return e.Name(), info, err
}
