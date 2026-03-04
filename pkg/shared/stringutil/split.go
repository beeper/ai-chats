package stringutil

import (
	"errors"
	"strings"
)

// SplitQuotedArgs parses a raw argument string into tokens, respecting
// single- and double-quoted segments and backslash escapes (except inside
// single quotes). Whitespace characters (space, tab, newline, carriage return)
// serve as delimiters outside of quotes.
func SplitQuotedArgs(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false

	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}

	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}

		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}

	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if escaped {
		current.WriteRune('\\')
	}
	flush()
	return args, nil
}
