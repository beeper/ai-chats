package utils

import (
	"strings"
	"unicode/utf8"
)

// SanitizeSurrogates removes unpaired UTF-16 surrogate code points.
func SanitizeSurrogates(text string) string {
	if text == "" {
		return text
	}

	var out strings.Builder
	runes := make([]rune, 0, len(text))
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 1 {
			text = text[size:]
			continue
		}
		runes = append(runes, r)
		text = text[size:]
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r >= 0xD800 && r <= 0xDBFF {
			if i+1 < len(runes) {
				next := runes[i+1]
				if next >= 0xDC00 && next <= 0xDFFF {
					out.WriteRune(r)
					out.WriteRune(next)
					i++
				}
			}
			continue
		}
		if r >= 0xDC00 && r <= 0xDFFF {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
