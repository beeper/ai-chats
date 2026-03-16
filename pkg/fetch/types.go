package fetch

import "context"

// Provider fetches readable content for a given backend.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, req Request) (*Response, error)
}

// Request represents a normalized fetch request.
type Request struct {
	URL         string
	ExtractMode string // "markdown" or "text"
	MaxChars    int
}

// Response represents normalized fetch output.
type Response struct {
	URL           string
	FinalURL      string
	Status        int
	ContentType   string
	ExtractMode   string
	Extractor     string
	Truncated     bool
	Length        int
	RawLength     int
	WrappedLength int
	FetchedAt     string
	TookMs        int64
	Text          string
	Warning       string
	Cached        bool
	Provider      string
	Extras        map[string]any
}
