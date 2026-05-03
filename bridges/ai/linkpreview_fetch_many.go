package ai

import (
	"context"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"strings"
	"sync"

	_ "golang.org/x/image/webp"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/citations"
)

func (lp *LinkPreviewer) FetchPreviews(ctx context.Context, urls []string) []*PreviewWithImage {
	if len(urls) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	results := make([]*PreviewWithImage, len(urls))

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, urlStr string) {
			defer wg.Done()
			preview, err := lp.FetchPreview(ctx, urlStr)
			if err == nil && preview != nil {
				results[idx] = preview
			}
		}(i, u)
	}

	wg.Wait()

	// Filter out nil results
	var previews []*PreviewWithImage
	for _, p := range results {
		if p != nil {
			previews = append(previews, p)
		}
	}
	return previews
}

// PreviewFromCitation builds a PreviewWithImage from a SourceCitation without fetching HTML.
// It downloads the image directly from the citation's Image URL.
func (lp *LinkPreviewer) PreviewFromCitation(ctx context.Context, urlStr string, c citations.SourceCitation) *PreviewWithImage {
	// Check cache first — a previous HTML-based fetch may have cached this URL.
	if cached := globalPreviewCache.get(urlStr); cached != nil {
		return cached
	}

	preview := &event.BeeperLinkPreview{
		LinkPreview: event.LinkPreview{
			CanonicalURL: c.URL,
			Title:        summarizeText(c.Title, 30, 150),
			Description:  summarizeText(c.Description, 50, 200),
			SiteName:     c.SiteName,
		},
		MatchedURL: urlStr,
	}
	if preview.CanonicalURL == "" {
		preview.CanonicalURL = urlStr
	}

	result := &PreviewWithImage{
		Preview: preview,
	}

	// Download image from citation's Image URL if available.
	imageURL := strings.TrimSpace(c.Image)
	if imageURL != "" {
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

	globalPreviewCache.set(urlStr, result, lp.config.CacheTTL)
	return result
}

// FetchPreviewsWithCitations fetches previews for multiple URLs, using SourceCitation
// metadata when available to skip HTML fetching.
func (lp *LinkPreviewer) FetchPreviewsWithCitations(ctx context.Context, urls []string, cits []citations.SourceCitation) []*PreviewWithImage {
	if len(urls) == 0 {
		return nil
	}

	// Build URL -> citation index for O(1) lookups.
	citationByURL := make(map[string]citations.SourceCitation, len(cits))
	for _, c := range cits {
		u := strings.TrimSpace(c.URL)
		if u != "" {
			citationByURL[u] = c
		}
	}

	var wg sync.WaitGroup
	results := make([]*PreviewWithImage, len(urls))

	for i, u := range urls {
		wg.Add(1)
		go func(idx int, urlStr string) {
			defer wg.Done()
			var preview *PreviewWithImage

			// Prefer citation-based preview when available.
			if c, ok := citationByURL[urlStr]; ok {
				preview = lp.PreviewFromCitation(ctx, urlStr, c)
				// If citation metadata has no image, try filling only the image via normal fetch.
				if preview != nil && strings.TrimSpace(c.Image) == "" {
					if fetched, err := lp.FetchPreview(ctx, urlStr); err == nil && fetched != nil {
						if len(fetched.ImageData) > 0 {
							preview.ImageData = fetched.ImageData
							preview.ImageURL = fetched.ImageURL
						}
						if preview.Preview != nil && fetched.Preview != nil {
							preview.Preview.ImageType = fetched.Preview.ImageType
							preview.Preview.ImageSize = fetched.Preview.ImageSize
							preview.Preview.ImageWidth = fetched.Preview.ImageWidth
							preview.Preview.ImageHeight = fetched.Preview.ImageHeight
							if preview.Preview.ImageURL == "" && fetched.Preview.ImageURL != "" {
								preview.Preview.ImageURL = fetched.Preview.ImageURL
							}
						}
					}
				}
			}

			// Fall back to HTML-based fetch.
			if preview == nil {
				if p, err := lp.FetchPreview(ctx, urlStr); err == nil {
					preview = p
				}
			}

			if preview != nil {
				results[idx] = preview
			}
		}(i, u)
	}

	wg.Wait()

	var previews []*PreviewWithImage
	for _, p := range results {
		if p != nil {
			previews = append(previews, p)
		}
	}
	return previews
}

// UploadPreviewImages uploads images from PreviewWithImage to Matrix and returns final BeeperLinkPreviews.
