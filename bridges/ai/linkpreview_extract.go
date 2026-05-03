package ai

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/PuerkitoBio/goquery"
	_ "golang.org/x/image/webp"
)

func extractTitle(doc *goquery.Document) string {
	// Try <title> tag first
	if title := doc.Find("title").First().Text(); title != "" {
		return strings.TrimSpace(title)
	}
	// Try h1
	if h1 := doc.Find("h1").First().Text(); h1 != "" {
		return strings.TrimSpace(h1)
	}
	// Try h2
	if h2 := doc.Find("h2").First().Text(); h2 != "" {
		return strings.TrimSpace(h2)
	}
	return ""
}

func extractDescription(doc *goquery.Document) string {
	// Try meta description
	if desc, exists := doc.Find("meta[name='description']").First().Attr("content"); exists && desc != "" {
		return strings.TrimSpace(desc)
	}
	// Try first paragraph
	if p := doc.Find("p").First().Text(); p != "" {
		return strings.TrimSpace(p)
	}
	return ""
}

func summarizeText(text string, maxWords, maxLength int) string {
	// Normalize whitespace
	text = strings.TrimSpace(text)
	text = whitespaceCollapseRE.ReplaceAllString(text, " ")

	if text == "" {
		return ""
	}

	// Limit words
	words := strings.Fields(text)
	if len(words) > maxWords {
		text = strings.Join(words[:maxWords], " ")
	}

	// Limit length
	if len(text) > maxLength {
		text = text[:maxLength]
		// Try to cut at word boundary
		if lastSpace := strings.LastIndex(text, " "); lastSpace > maxLength/2 {
			text = text[:lastSpace]
		}
		text += "..."
	}

	return text
}
