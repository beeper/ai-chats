package connector

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"maunium.net/go/mautrix/event"
)

var (
	fileCloseTagRE = regexp.MustCompile(`(?i)<\s*/\s*file\s*>`)
	fileOpenTagRE  = regexp.MustCompile(`(?i)<\s*file\b`)
)

const maxTextFileBytes = 5 * 1024 * 1024

func normalizeMimeType(mimeType string) string {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	if lower == "" {
		return lower
	}
	if semi := strings.IndexByte(lower, ';'); semi >= 0 {
		return strings.TrimSpace(lower[:semi])
	}
	return lower
}

var textFileMimeTypesMap = map[string]event.CapabilitySupportLevel{
	"text/plain":                event.CapLevelFullySupported,
	"text/markdown":             event.CapLevelFullySupported,
	"text/csv":                  event.CapLevelFullySupported,
	"text/tab-separated-values": event.CapLevelFullySupported,
	"text/html":                 event.CapLevelFullySupported,
	"text/xml":                  event.CapLevelFullySupported,
	"text/yaml":                 event.CapLevelFullySupported,
	"text/javascript":           event.CapLevelFullySupported,
	"application/json":          event.CapLevelFullySupported,
	"application/xml":           event.CapLevelFullySupported,
	"application/xhtml+xml":     event.CapLevelFullySupported,
	"application/x-yaml":        event.CapLevelFullySupported,
	"application/yaml":          event.CapLevelFullySupported,
	"application/toml":          event.CapLevelFullySupported,
	"application/x-toml":        event.CapLevelFullySupported,
	"application/csv":           event.CapLevelFullySupported,
	"application/x-csv":         event.CapLevelFullySupported,
	"application/javascript":    event.CapLevelFullySupported,
}

func isTextFileMime(mimeType string) bool {
	mimeType = normalizeMimeType(mimeType)
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	if strings.HasSuffix(mimeType, "+json") || strings.HasSuffix(mimeType, "+xml") || strings.HasSuffix(mimeType, "+yaml") {
		return true
	}
	_, ok := textFileMimeTypesMap[mimeType]
	return ok
}

func decodeBase64String(b64Data string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err == nil {
		return data, nil
	}
	data, err = base64.URLEncoding.DecodeString(b64Data)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}
	return data, nil
}

func trimTextForModel(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if len(text) <= AIMaxTextLength {
		return text, false
	}
	return text[:AIMaxTextLength] + "...", true
}

var cp1252Map = []rune{
	'\u20ac', 0, '\u201a', '\u0192', '\u201e', '\u2026', '\u2020', '\u2021',
	'\u02c6', '\u2030', '\u0160', '\u2039', '\u0152', 0, '\u017d', 0,
	0, '\u2018', '\u2019', '\u201c', '\u201d', '\u2022', '\u2013', '\u2014',
	'\u02dc', '\u2122', '\u0161', '\u203a', '\u0153', 0, '\u017e', '\u0178',
}

func decodeCP1252(data []byte) string {
	var b strings.Builder
	b.Grow(len(data))
	for _, v := range data {
		if v >= 0x80 && v <= 0x9f {
			r := cp1252Map[v-0x80]
			if r == 0 {
				b.WriteByte(v)
			} else {
				b.WriteRune(r)
			}
			continue
		}
		b.WriteByte(v)
	}
	return b.String()
}

func detectUTF16(data []byte) (string, int) {
	if len(data) < 2 {
		return "", 0
	}
	if data[0] == 0xff && data[1] == 0xfe {
		return "utf-16le", 2
	}
	if data[0] == 0xfe && data[1] == 0xff {
		return "utf-16be", 2
	}
	sampleLen := len(data)
	if sampleLen > 2048 {
		sampleLen = 2048
	}
	if sampleLen < 2 {
		return "", 0
	}
	zeroEven := 0
	zeroOdd := 0
	for i := 0; i < sampleLen; i++ {
		if data[i] != 0 {
			continue
		}
		if i%2 == 0 {
			zeroEven++
		} else {
			zeroOdd++
		}
	}
	zeroCount := zeroEven + zeroOdd
	if float64(zeroCount)/float64(sampleLen) > 0.2 {
		if zeroOdd >= zeroEven {
			return "utf-16le", 0
		}
		return "utf-16be", 0
	}
	return "", 0
}

func decodeUTF16(data []byte, order binary.ByteOrder) string {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	u16s := make([]uint16, len(data)/2)
	for i := 0; i < len(u16s); i++ {
		u16s[i] = order.Uint16(data[i*2 : i*2+2])
	}
	return string(utf16.Decode(u16s))
}

func isMostlyPrintable(text string) bool {
	total := 0
	printable := 0
	for _, r := range text {
		total++
		if r == '\uFFFD' {
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsPrint(r) {
			printable++
		}
	}
	if total == 0 {
		return false
	}
	return float64(printable)/float64(total) >= 0.7
}

func decodeTextBytes(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if utf8.Valid(data) {
		return string(data), nil
	}
	if encoding, bom := detectUTF16(data); encoding != "" {
		payload := data
		if bom > 0 && len(data) >= bom {
			payload = data[bom:]
		}
		if encoding == "utf-16le" {
			return decodeUTF16(payload, binary.LittleEndian), nil
		}
		return decodeUTF16(payload, binary.BigEndian), nil
	}

	text := decodeCP1252(data)
	if isMostlyPrintable(text) {
		return text, nil
	}
	return "", errors.New("file is not valid text")
}

func (oc *AIClient) downloadTextFile(ctx context.Context, mediaURL string, encryptedFile *event.EncryptedFileInfo, mimeType string) (string, bool, error) {
	b64Data, _, err := oc.downloadMediaBase64(ctx, mediaURL, encryptedFile, maxTextFileBytes/(1024*1024), mimeType)
	if err != nil {
		return "", false, err
	}
	data, err := decodeBase64String(b64Data)
	if err != nil {
		return "", false, err
	}
	if len(data) > maxTextFileBytes {
		return "", false, fmt.Errorf("file too large (%d bytes)", len(data))
	}
	text, err := decodeTextBytes(data)
	if err != nil {
		return "", false, err
	}
	trimmed, truncated := trimTextForModel(text)
	return trimmed, truncated, nil
}

func buildTextFileMessage(caption string, hasUserCaption bool, filename string, mimeType string, content string, truncated bool) string {
	_ = truncated
	if !hasUserCaption {
		caption = ""
	}
	caption = strings.TrimSpace(caption)
	filename = strings.TrimSpace(filename)
	mimeType = strings.TrimSpace(mimeType)
	if filename == "" {
		filename = "file"
	}
	if mimeType == "" {
		mimeType = "text/plain"
	}

	block := fmt.Sprintf(
		"<file name=\"%s\" mime=\"%s\">\n%s\n</file>",
		xmlEscapeAttr(filename),
		xmlEscapeAttr(mimeType),
		escapeFileBlockContent(content),
	)

	if caption == "" {
		return block
	}
	return strings.TrimSpace(caption + "\n\n" + block)
}

var xmlEscapeMap = map[rune]string{
	'<':  "&lt;",
	'>':  "&gt;",
	'&':  "&amp;",
	'"':  "&quot;",
	'\'': "&apos;",
}

func xmlEscapeAttr(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if repl, ok := xmlEscapeMap[r]; ok {
			b.WriteString(repl)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escapeFileBlockContent(value string) string {
	value = fileCloseTagRE.ReplaceAllString(value, "&lt;/file&gt;")
	value = fileOpenTagRE.ReplaceAllString(value, "&lt;file")
	return value
}
