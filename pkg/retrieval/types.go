package retrieval

import "context"

// SearchProvider fetches normalized search results from a backend.
type SearchProvider interface {
	Name() string
	Search(ctx context.Context, req SearchRequest) (*SearchResponse, error)
}

// FetchProvider fetches readable content for a given backend.
type FetchProvider interface {
	Name() string
	Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error)
}

// SearchRequest represents a normalized web search request.
type SearchRequest struct {
	Query      string
	Count      int
	Country    string
	SearchLang string
	UILang     string
	Freshness  string
}

// SearchResult is a normalized search result.
type SearchResult struct {
	ID          string
	Title       string
	URL         string
	Description string
	Published   string
	SiteName    string
	Author      string
	Image       string
	Favicon     string
}

// SearchResponse is a normalized search response.
type SearchResponse struct {
	Query      string
	Provider   string
	Count      int
	TookMs     int64
	Results    []SearchResult
	Answer     string
	Summary    string
	Definition string
	Warning    string
	NoResults  bool
	Cached     bool
	Extras     map[string]any
}

// FetchRequest represents a normalized fetch request.
type FetchRequest struct {
	URL         string
	ExtractMode string // "markdown" or "text"
	MaxChars    int
}

// FetchResponse represents normalized fetch output.
type FetchResponse struct {
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
