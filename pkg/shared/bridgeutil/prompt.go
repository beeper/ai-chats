package bridgeutil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// PromptLine prints label to stdout and reads a single trimmed line from stdin.
func PromptLine(label string) (string, error) {
	fmt.Fprint(os.Stdout, label)
	r := bufio.NewReader(os.Stdin)
	s, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(s), nil
}
