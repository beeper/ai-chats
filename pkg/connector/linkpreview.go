package connector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dyatlov/go-opengraph/opengraph"
	_ "golang.org/x/image/webp"
	"maunium.net/go/mautrix/event"
)

type LinkPreviewConfig struct {
	Enabled         bool          `yaml:"enabled"`
	MaxURLsInbound  int           `yaml:"max_urls_inbound"`  // Max URLs to process from user messages
	MaxURLsOutbound int           `yaml:"max_urls_outbound"` // Max URLs to preview in AI responses
	FetchTimeout    time.Duration `yaml:"fetch_timeout"`     // Timeout for fetching each URL
	MaxContentChars int           `yaml:"max_content_chars"` // Max chars for description in context
	MaxPageBytes    int64         `yaml:"max_page_bytes"`    // Max page size to download
	MaxImageBytes   int64         `yaml:"max_image_bytes"`   // Max image size to download
	CacheTTL        time.Duration `yaml:"cache_ttl"`         // How long to cache previews
}

func DefaultLinkPreviewConfig() LinkPreviewConfig {
	return LinkPreviewConfig{
		Enabled:         true,
		MaxURLsInbound:  3,
		MaxURLsOutbound: 5,
		FetchTimeout:    10 * time.Second,
		MaxContentChars: 500,
		MaxPageBytes:    10 * 1024 * 1024, // 10MB
		MaxImageBytes:   5 * 1024 * 1024,  // 5MB
		CacheTTL:        1 * time.Hour,
	}
}

// PreviewWithImage holds a preview along with its downloaded image data.
type PreviewWithImage struct {
	Preview   *event.BeeperLinkPreview
	ImageData []byte
	ImageURL  string // Original image URL for reference
}

type previewCacheEntry struct {
	preview   *PreviewWithImage
	expiresAt time.Time
}

type previewCache struct {
	mu      sync.RWMutex
	entries map[string]*previewCacheEntry
}

var globalPreviewCache = &previewCache{
	entries: make(map[string]*previewCacheEntry),
}

func cloneBeeperLinkPreview(src *event.BeeperLinkPreview) *event.BeeperLinkPreview {
	if src == nil {
		return nil
	}
	clone := *src
	return &clone
}

func clonePreviewWithImage(src *PreviewWithImage) *PreviewWithImage {
	if src == nil {
		return nil
	}
	clone := &PreviewWithImage{
		Preview:  cloneBeeperLinkPreview(src.Preview),
		ImageURL: src.ImageURL,
	}
	clone.ImageData = slices.Clone(src.ImageData)
	return clone
}

func (c *previewCache) get(url string) *PreviewWithImage {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[url]
	if !ok {
		return nil
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, url)
		return nil
	}
	return clonePreviewWithImage(entry.preview)
}

func (c *previewCache) set(url string, preview *PreviewWithImage, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[url] = &previewCacheEntry{
		preview:   clonePreviewWithImage(preview),
		expiresAt: time.Now().Add(ttl),
	}

	// Simple cleanup: remove expired entries if cache is getting large
	if len(c.entries) > 1000 {
		now := time.Now()
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
}

type LinkPreviewer struct {
	config     LinkPreviewConfig
	httpClient *http.Client
}

func NewLinkPreviewer(config LinkPreviewConfig) *LinkPreviewer {
	return &LinkPreviewer{
		config: config,
		httpClient: &http.Client{
			Timeout: config.FetchTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
}

// URL matching regex - matches http/https URLs
var urlRegex = regexp.MustCompile(`https?://[^\s<>\[\]()'"]+[^\s<>\[\]()'",.:;!?]`)

// Markdown link regex to strip [text](url) before extracting bare URLs
var markdownLinkRegex = regexp.MustCompile(`\[[^\]]*]\((https?://\S+?)\)`)

var whitespaceCollapseRE = regexp.MustCompile(`\s+`)

// ExtractURLs extracts URLs from text, returning up to maxURLs unique URLs.
// It strips markdown link syntax to avoid detecting the same URL twice.
func ExtractURLs(text string, maxURLs int) []string {
	if maxURLs <= 0 {
		return nil
	}

	// Strip markdown links so only bare URLs are considered
	sanitized := markdownLinkRegex.ReplaceAllString(text, " ")

	matches := urlRegex.FindAllString(sanitized, -1)
	if len(matches) == 0 {
		return nil
	}

	// Deduplicate, filter, and limit
	seen := make(map[string]bool)
	var urls []string
	for _, match := range matches {
		// Clean up trailing punctuation that might have been captured
		cleaned := strings.TrimRight(match, ".,;:!?")
		if seen[cleaned] {
			continue
		}
		if !isAllowedURL(cleaned) {
			continue
		}
		seen[cleaned] = true
		urls = append(urls, cleaned)
		if len(urls) >= maxURLs {
			break
		}
	}
	return urls
}

func isAllowedURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return false
		}
	}
	return true
}

// FetchPreview fetches and generates a link preview for a URL, including the image data.
func (lp *LinkPreviewer) FetchPreview(ctx context.Context, urlStr string) (*PreviewWithImage, error) {
	// Check cache first
	if cached := globalPreviewCache.get(urlStr); cached != nil {
		return cached, nil
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme: %s", parsedURL.Scheme)
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers to look like a browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := lp.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml") {
		return nil, fmt.Errorf("unsupported content type: %s", contentType)
	}

	// Read body with size limit
	limitedReader := io.LimitReader(resp.Body, lp.config.MaxPageBytes)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse with OpenGraph
	og := opengraph.NewOpenGraph()
	if err := og.ProcessHTML(strings.NewReader(string(body))); err != nil {
		return nil, fmt.Errorf("failed to parse OpenGraph: %w", err)
	}

	// Fallback parsing with goquery if OpenGraph data is incomplete
	var doc *goquery.Document
	if og.Title == "" || og.Description == "" {
		doc, err = goquery.NewDocumentFromReader(strings.NewReader(string(body)))
		if err == nil {
			if og.Title == "" {
				og.Title = extractTitle(doc)
			}
			if og.Description == "" {
				og.Description = extractDescription(doc)
			}
		}
	}

	// Build preview
	preview := &event.BeeperLinkPreview{
		LinkPreview: event.LinkPreview{
			CanonicalURL: og.URL,
			Title:        summarizeText(og.Title, 30, 150),
			Type:         og.Type,
			Description:  summarizeText(og.Description, 50, 200),
			SiteName:     og.SiteName,
		},
		MatchedURL: urlStr,
	}

	// Use the original URL if canonical is empty
	if preview.CanonicalURL == "" {
		preview.CanonicalURL = urlStr
	}

	result := &PreviewWithImage{
		Preview: preview,
	}

	// Download og:image if available
	if len(og.Images) > 0 && og.Images[0].URL != "" {
		imageURL := og.Images[0].URL
		// Resolve relative URLs
		if !strings.HasPrefix(imageURL, "http") {
			if base, err := url.Parse(urlStr); err == nil {
				if rel, err := url.Parse(imageURL); err == nil {
					imageURL = base.ResolveReference(rel).String()
				}
			}
		}

		imageData, mimeType, width, height := lp.downloadImage(ctx, imageURL)
		if imageData != nil {
			result.ImageData = imageData
			result.ImageURL = imageURL
			preview.ImageType = mimeType
			preview.ImageSize = event.IntOrString(len(imageData))
			preview.ImageWidth = event.IntOrString(width)
			preview.ImageHeight = event.IntOrString(height)
		}
	}

	// Cache the result
	globalPreviewCache.set(urlStr, result, lp.config.CacheTTL)

	return result, nil
}

func (lp *LinkPreviewer) downloadImage(ctx context.Context, imageURL string) ([]byte, string, int, int) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", 0, 0
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "image/*")

	resp, err := lp.httpClient.Do(req)
	if err != nil {
		return nil, "", 0, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", 0, 0
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", 0, 0
	}

	// Read with size limit
	maxBytes := lp.config.MaxImageBytes
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024 // 5MB default
	}
	limitedReader := io.LimitReader(resp.Body, maxBytes)
	data, err := io.ReadAll(limitedReader)
	if err != nil || len(data) == 0 {
		return nil, "", 0, 0
	}

	// Detect actual mime type
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", 0, 0
	}

	// Get dimensions
	width, height := 0, 0
	if img, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		width = img.Width
		height = img.Height
	}

	return data, mimeType, width, height
}

// FetchPreviews fetches previews for multiple URLs in parallel.
