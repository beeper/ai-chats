package retrieval

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

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

type directFetchProvider struct {
	cfg DirectConfig
}

func newDirectFetchProvider(cfg *FetchConfig) FetchProvider {
	if cfg == nil {
		return nil
	}
	if !stringutil.BoolPtrOr(cfg.Direct.Enabled, true) {
		return nil
	}
	return &directFetchProvider{cfg: cfg.Direct}
}

func (p *directFetchProvider) Name() string {
	return ProviderDirect
}

func (p *directFetchProvider) Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error) {
	if !isAllowedURL(req.URL) {
		return nil, errors.New("url not allowed")
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
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxChars*2)))
	if err != nil {
		return nil, err
	}

	contentType := normalizeContentType(resp.Header.Get("Content-Type"))
	text := string(body)
	extractor := "basic"
	switch {
	case strings.Contains(contentType, "text/html"):
		text = extractTextFromHTML(text)
		if strings.EqualFold(req.ExtractMode, "text") {
			extractor = "basic-text"
		} else {
			extractor = "basic-markdown"
		}
	case strings.Contains(contentType, "application/json"):
		var decoded any
		if err := json.Unmarshal(body, &decoded); err == nil {
			pretty, _ := json.MarshalIndent(decoded, "", "  ")
			text = string(pretty)
			extractor = "json"
		}
	}

	rawLength := len(text)
	truncated := maxChars > 0 && len(text) > maxChars
	if truncated {
		text = text[:maxChars] + "...[truncated]"
	}

	finalURL := req.URL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	return &FetchResponse{
		URL:           req.URL,
		FinalURL:      finalURL,
		Status:        resp.StatusCode,
		ContentType:   contentType,
		ExtractMode:   req.ExtractMode,
		Extractor:     extractor,
		Truncated:     truncated,
		Length:        len(text),
		RawLength:     rawLength,
		WrappedLength: len(text),
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
	ct, _, _ := strings.Cut(value, ";")
	return strings.TrimSpace(ct)
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

func extractTextFromHTML(html string) string {
	for _, tag := range []string{"script", "style", "noscript"} {
		html = removeHTMLElement(html, tag)
	}

	var result strings.Builder
	inTag := false
	lastWasSpace := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			if !lastWasSpace {
				result.WriteRune(' ')
				lastWasSpace = true
			}
		case inTag:
			// skip characters inside tags
		case r == '\n' || r == '\r' || r == '\t' || r == ' ':
			if !lastWasSpace {
				result.WriteRune(' ')
				lastWasSpace = true
			}
		default:
			result.WriteRune(r)
			lastWasSpace = false
		}
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
		lower = lower[:start] + lower[end:]
	}
	return html
}
