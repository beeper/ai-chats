package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type directProvider struct {
	cfg DirectConfig
}

func newDirectProvider(cfg *Config) Provider {
	if cfg == nil {
		return nil
	}
	if !isEnabled(cfg.Direct.Enabled, true) {
		return nil
	}
	return &directProvider{cfg: cfg.Direct}
}

func (p *directProvider) Name() string {
	return ProviderDirect
}

func (p *directProvider) Fetch(ctx context.Context, req Request) (*Response, error) {
	if !isAllowedURL(req.URL) {
		return nil, errors.New("url not allowed")
	}
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, errors.New("url must use http or https")
	}

	client := &http.Client{Timeout: time.Duration(p.cfg.TimeoutSecs) * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", p.cfg.UserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	start := time.Now()
	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = p.cfg.MaxChars
		if maxChars <= 0 {
			maxChars = DefaultMaxChars
		}
	}

	limit := int64(maxChars * 2)
	if limit <= 0 {
		limit = int64(DefaultMaxChars * 2)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}

	contentType := normalizeContentType(resp.Header.Get("Content-Type"))
	text := string(body)
	extractor := "basic"
	if strings.Contains(contentType, "text/html") {
		if strings.EqualFold(req.ExtractMode, "text") {
			text = extractTextFromHTML(text)
			extractor = "basic-text"
		} else {
			text = htmlToMarkdownBasic(text)
			extractor = "basic-markdown"
		}
	} else if strings.Contains(contentType, "application/json") {
		var decoded any
		if err := json.Unmarshal(body, &decoded); err == nil {
			pretty, _ := json.MarshalIndent(decoded, "", "  ")
			text = string(pretty)
			extractor = "json"
		}
	}

	truncated := false
	rawLength := len(text)
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars] + "...[truncated]"
		truncated = true
	}
	wrappedLength := len(text)

	finalURL := req.URL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	return &Response{
		URL:           req.URL,
		FinalURL:      finalURL,
		Status:        resp.StatusCode,
		ContentType:   contentType,
		ExtractMode:   req.ExtractMode,
		Extractor:     extractor,
		Truncated:     truncated,
		Length:        len(text),
		RawLength:     rawLength,
		WrappedLength: wrappedLength,
		FetchedAt:     time.Now().UTC().Format(time.RFC3339),
		TookMs:        time.Since(start).Milliseconds(),
		Text:          text,
		Provider:      ProviderDirect,
	}, nil
}

func normalizeContentType(value string) string {
	if value == "" {
		return "application/octet-stream"
	}
	parts := strings.Split(value, ";")
	return strings.TrimSpace(parts[0])
}

var fetchBlockedCIDRs = []*net.IPNet{
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("::1/128"),
}

func mustParseCIDR(value string) *net.IPNet {
	_, parsed, err := net.ParseCIDR(value)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", value, err))
	}
	return parsed
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
		if ip4 := ip.To4(); ip4 != nil {
			ip = ip4
		}
		for _, cidr := range fetchBlockedCIDRs {
			if cidr.Contains(ip) {
				return false
			}
		}
	}
	return true
}

func htmlToMarkdownBasic(input string) string {
	// Minimal HTML stripping for now; can be improved with readability later.
	return extractTextFromHTML(input)
}

func extractTextFromHTML(html string) string {
	html = removeHTMLElement(html, "script")
	html = removeHTMLElement(html, "style")
	html = removeHTMLElement(html, "noscript")

	var result strings.Builder
	inTag := false
	lastWasSpace := false
	for _, r := range html {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			if !lastWasSpace {
				result.WriteRune(' ')
				lastWasSpace = true
			}
			continue
		}
		if inTag {
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			if !lastWasSpace {
				result.WriteRune(' ')
				lastWasSpace = true
			}
			continue
		}
		result.WriteRune(r)
		lastWasSpace = false
	}
	return strings.TrimSpace(result.String())
}

func removeHTMLElement(html, tag string) string {
	lower := strings.ToLower(html)
	openTag := "<" + tag
	closeTag := "</" + tag + ">"
	for {
		start := strings.Index(lower, openTag)
		if start == -1 {
			break
		}
		end := strings.Index(lower[start:], closeTag)
		if end == -1 {
			break
		}
		end += start + len(closeTag)
		html = html[:start] + html[end:]
		lower = strings.ToLower(html)
	}
	return html
}
